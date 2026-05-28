package handlers

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/downloads"
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
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", hlsVODSegDur+1)
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
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n", d)
		fmt.Fprintf(&b, "seg_%05d.ts%s\n", i, q)
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
func StreamHLSMaster(s *streamer.Streamer, mgr *transcode.HLSSessionManager, store *downloads.Store) gin.HandlerFunc {
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

		transcodeSource, transcodeSourceSize := resolveTranscodeSource(c, s, store, h, fileIdx)
		if transcodeSource == nil {
			return
		}

		key := fmt.Sprintf("%s-%d", h.HexString(), fileIdx)
		sess, err := mgr.GetOrStart(c.Request.Context(), transcode.HLSStartOpts{
			Key:        key,
			Source:     transcodeSource,
			SourceSize: transcodeSourceSize,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if !waitForMasterPlaylist(c, s, sess, h, fileIdx) {
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

// resolveTranscodeSource tries the completed-download store first, then falls
// back to activating the torrent and opening a streaming reader.
func resolveTranscodeSource(c *gin.Context, s *streamer.Streamer, store *downloads.Store, h metainfo.Hash, fileIdx int) (io.ReadSeekCloser, int64) {
	if store != nil {
		if path, err := store.GetCompletedPath(h.HexString(), fileIdx); err == nil && path != "" {
			if stat, err := os.Stat(path); err == nil {
				if f, err := os.Open(path); err == nil {
					return f, stat.Size()
				}
			}
		}
	}
	if _, err := s.Get(h); err != nil {
		bareMagnet := "magnet:?xt=urn:btih:" + h.HexString()
		if _, addErr := s.Add(c.Request.Context(), bareMagnet); addErr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return nil, 0
		}
	}
	reader, file, err := s.FileReader(h, fileIdx)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return nil, 0
	}
	return reader, file.Length()
}

// waitForMasterPlaylist blocks until the first HLS segment is ready. On failure
// it classifies the reason (no_seeds, slow_download) and responds with 503.
func waitForMasterPlaylist(c *gin.Context, s *streamer.Streamer, sess *transcode.HLSSession, h metainfo.Hash, fileIdx int) bool {
	if err := sess.WaitForMaster(2 * time.Minute); err != nil {
		resp := gin.H{"error": err.Error(), "code": "transcode_failed"}
		if info, gerr := s.Get(h); gerr == nil {
			resp["downRate"] = info.DownRate
			resp["peers"] = info.Peers
			if fileIdx >= 0 && fileIdx < len(info.Files) {
				resp["fileProgress"] = info.Files[fileIdx].Progress
				downloaded := info.Files[fileIdx].Downloaded
				switch {
				case info.Peers == 0:
					resp["code"] = "no_seeds"
				case downloaded < 30<<20:
					resp["code"] = "slow_download"
				}
			}
		}
		c.JSON(http.StatusServiceUnavailable, resp)
		return false
	}
	return true
}
