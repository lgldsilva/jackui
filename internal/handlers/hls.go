package handlers

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/streamer"
	"github.com/luizg/jackui/internal/transcode"
)

// hlsVODSegDur must match transcode.hlsSegDur — the segment length the encoder
// targets with forced keyframes. The synthesised playlist declares each
// segment as this long so Safari's timeline (sum of EXTINF) matches the media.
const hlsVODSegDur = 4

// buildVODPlaylist synthesises a finite HLS playlist covering the whole media
// duration: every segment is declared up front (with a token on each line) and
// EXT-X-ENDLIST marks it complete, so Safari renders a full seekbar instead of
// treating the stream as headless LIVE. Segments the encoder hasn't produced
// yet are generated on demand (seek-restart) when the player requests them.
func buildVODPlaylist(durationSec float64, token string) []byte {
	n := int(math.Ceil(durationSec / hlsVODSegDur))
	if n < 1 {
		n = 1
	}
	q := ""
	if token != "" {
		q = "?token=" + token
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:6\n")
	// TARGETDURATION must be >= the longest EXTINF; segments are ~4s but allow
	// slack for the trailing partial segment and minor keyframe rounding.
	b.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", hlsVODSegDur+1))
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	for i := 0; i < n; i++ {
		d := float64(hlsVODSegDur)
		if i == n-1 {
			if last := durationSec - float64(i*hlsVODSegDur); last > 0 && last < d {
				d = last
			}
		}
		b.WriteString(fmt.Sprintf("#EXTINF:%.3f,\n", d))
		b.WriteString(fmt.Sprintf("seg_%05d.ts%s\n", i, q))
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String())
}

// StreamHLSMaster handles GET /api/stream/hls/:hash/:file/index.m3u8 —
// kicks off (or attaches to) the HLS transcoding session and returns the
// playlist. Safari fetches this first; segment requests follow.
//
// Why HLS for Safari specifically: progressive fragmented MP4 via chunked
// transfer is rejected by Safari's MSE pipeline with MediaError.SRC_NOT_SUPPORTED
// regardless of how we tune the encode. Apple's documented streaming path is
// HLS (.m3u8 + .ts) and `<video src="...m3u8">` is the only thing Safari
// treats as a seekable / progressive video source. Jellyfin, Plex, Emby all
// use HLS for browser playback for the same reason.
func StreamHLSMaster(s *streamer.Streamer, mgr *transcode.HLSSessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file index"})
			return
		}

		// Make sure the torrent is active; auto-add via bare magnet if it
		// got dropped after a deploy or idle GC (mirrors the M3U handler).
		if _, err := s.Get(h); err != nil {
			bareMagnet := "magnet:?xt=urn:btih:" + h.HexString()
			if _, addErr := s.Add(c.Request.Context(), bareMagnet); addErr != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			}
		}

		reader, file, err := s.FileReader(h, fileIdx)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		// IMPORTANT: don't `defer reader.Close()` here — the reader is
		// handed to ffmpeg which lives across this request's lifetime. We
		// rely on ffmpeg exit to release the underlying anacrolix Reader.

		key := fmt.Sprintf("%s-%d", h.HexString(), fileIdx)
		sess, err := mgr.GetOrStart(c.Request.Context(), transcode.HLSStartOpts{
			Key:        key,
			Source:     reader,
			SourceSize: file.Length(),
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// First segment appears ~4s after ffmpeg starts on 1080p. Large HEVC
		// MKVs over a torrent need much longer: the demuxer reads the header,
		// seeks to the Cues at EOF, then has to pull enough video clusters to
		// fill a 4s segment — all gated on piece arrival. 5 min is generous but
		// "slow to download" must NOT be treated as failure (user's call). The
		// session stays alive the whole time (WaitForMaster bumps LastAccess so
		// the gcLoop won't reap it), and the buffering overlay shows live
		// progress so the wait is legible rather than a frozen spinner.
		if err := sess.WaitForMaster(2 * time.Minute); err != nil {
			// Classify WHY it failed so the UI can show an honest message
			// instead of the misleading "codec/container não compatível".
			// A healthy swarm delivers enough for the first segment within 90s;
			// if barely anything downloaded, the bottleneck is the swarm, not
			// the codec. Surface real metrics (rate, %, peers) either way.
			resp := gin.H{"error": err.Error(), "code": "transcode_failed"}
			if info, gerr := s.Get(h); gerr == nil {
				resp["downRate"] = info.DownRate
				resp["peers"] = info.Peers
				var fileDownloaded int64
				if fileIdx >= 0 && fileIdx < len(info.Files) {
					resp["fileProgress"] = info.Files[fileIdx].Progress
					fileDownloaded = info.Files[fileIdx].Downloaded
				}
				switch {
				case info.Peers == 0:
					resp["code"] = "no_seeds"
				case fileDownloaded < 30<<20: // < 30 MB after 90s ⇒ starving
					resp["code"] = "slow_download"
				}
			}
			c.JSON(http.StatusServiceUnavailable, resp)
			return
		}

		// VOD mode (gated by hlsVODEnabled): serve a synthesised finite playlist
		// (full seekbar) instead of ffmpeg's incremental EVENT one. When VOD is
		// off, fall through to the EVENT path — the proven, reliable transcode
		// playback for HEVC/fallback sources.
		if sess.IsVOD() {
			c.Header("Cache-Control", "no-store")
			c.Data(http.StatusOK, "application/vnd.apple.mpegurl",
				buildVODPlaylist(sess.DurationSec, c.Query("token")))
			return
		}

		// EVENT fallback (duration unknown): Safari resolves relative segment
		// names against the playlist URL but DROPS the query string — so
		// `?token=...` from the master URL does NOT reach the segment request
		// and the JWT middleware returns 401. We read ffmpeg's playlist and
		// append `?token=XXX` to each segment line so they authenticate.
		data, err := os.ReadFile(filepath.Join(sess.Dir, "index.m3u8"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "playlist not readable"})
			return
		}
		if tok := c.Query("token"); tok != "" {
			lines := strings.Split(string(data), "\n")
			for i, line := range lines {
				trim := strings.TrimSpace(line)
				if trim == "" || strings.HasPrefix(trim, "#") {
					continue
				}
				lines[i] = trim + "?token=" + tok
			}
			data = []byte(strings.Join(lines, "\n"))
		}
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "application/vnd.apple.mpegurl", data)
	}
}

// StreamHLSSegment handles GET /api/stream/hls/:hash/:file/:seg — serves
// one .ts segment from the per-session disk cache. Waits briefly if the
// segment hasn't been encoded yet (typical at the playback edge).
func StreamHLSSegment(mgr *transcode.HLSSessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file index"})
			return
		}
		segName := c.Param("seg")

		key := fmt.Sprintf("%s-%d", h.HexString(), fileIdx)
		// Re-attach to the existing session. If it's gone (cleanup ran
		// during a network pause), the playlist request will respawn it
		// when the client retries — segment lookups don't restart encode.
		sess, err := getSession(mgr, key)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not active — request the playlist again"})
			return
		}

		// VOD seek: if the requested segment isn't encoded yet, make sure an
		// encoder is producing it (backward seek or a forward jump beyond the
		// look-ahead window triggers a seek-restart). No-op in EVENT mode.
		if sess.IsVOD() {
			if idx, ok := transcode.ParseSegIndex(segName); ok {
				if _, statErr := os.Stat(filepath.Join(sess.Dir, segName)); statErr != nil {
					sess.EnsureSegment(idx)
				}
			}
		}

		path, err := sess.WaitForSegment(segName, 30*time.Second)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}

		// Long-lived cache header: segments don't change mid-session.
		c.Header("Content-Type", "video/mp2t")
		c.Header("Cache-Control", "max-age=3600")
		c.File(path)
	}
}

// getSession is a small helper to look up an existing session without
// going through the start path. Avoids creating a duplicate ffmpeg if the
// client races and hits the segment handler before the playlist handler.
func getSession(mgr *transcode.HLSSessionManager, key string) (*transcode.HLSSession, error) {
	// Manager exposes only GetOrStart; we cheat by passing a nil-ish opts
	// but it'll dedupe on Key. The downside is theoretical: if the session
	// was reaped and the segment request arrived first, we'd start a new
	// ffmpeg without a Source. Mitigate by requiring Source non-nil there.
	// For simplicity, return an error here when the session isn't already
	// tracked — clients refetch the playlist which respawns properly.
	return mgr.Peek(key)
}
