package handlers

import (
	"fmt"
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

		// First segment + playlist appear ~4s after ffmpeg starts on 1080p.
		// 4K sources need longer: each segment pulls ~15 MB and the encoder
		// can't emit the playlist until the first segment is fully written.
		// 90s gives margin for 4K piece arrival + NVENC startup on a healthy
		// swarm, while still surfacing a genuinely stuck torrent.
		if err := sess.WaitForMaster(90 * time.Second); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}

		// Safari resolves relative segment names against the playlist URL
		// but DROPS the query string in the process — so `?token=...` from
		// `/api/stream/hls/HASH/IDX/index.m3u8?token=XXX` does NOT reach
		// `/api/stream/hls/HASH/IDX/seg_00000.ts`, and the JWT middleware
		// returns 401. We read the playlist and append `?token=XXX` to each
		// segment line so segment requests authenticate independently.
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
