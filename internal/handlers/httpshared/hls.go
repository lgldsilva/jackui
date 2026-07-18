package httpshared

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/transcode"
)

// HLSVODSegDur must match transcode.hlsSegDur — the segment length the encoder
// targets with forced keyframes. The synthesised playlist declares each
// segment as this long so Safari's timeline (sum of EXTINF) matches the media.
const HLSVODSegDur = 4

// NativeHLSParam reads the client-class flag the frontend appends to HLS URLs
// (1 = Safari/iOS native HLS). Drives the VOD policy + session keying.
func NativeHLSParam(c *gin.Context) bool { return c.Query("native_hls") == "1" }

// PlaybackSession returns a filesystem-safe, client-generated playback ID.
// It is deliberately separate from authentication: its only purpose is to
// isolate HLS encoders when different viewers seek through the same media.
func PlaybackSession(c *gin.Context) string {
	v := c.Query("playback")
	if len(v) == 0 || len(v) > 64 {
		return ""
	}
	for _, r := range v {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return ""
		}
	}
	return v
}

// PlaybackSessionSuffix returns the stable session-key suffix for this
// playback, or empty for legacy clients that do not send a playback ID.
func PlaybackSessionSuffix(c *gin.Context) string {
	if v := PlaybackSession(c); v != "" {
		return "-p" + v
	}
	return ""
}

// EnsureVODSegment forces the encoder to produce segName if the session is in
// VOD mode and the segment isn't on disk yet (seek-restart on demand).
func EnsureVODSegment(sess *transcode.HLSSession, segName string) {
	if !sess.IsVOD() {
		return
	}
	idx, ok := transcode.ParseSegIndex(segName)
	if !ok {
		return
	}
	if _, statErr := os.Stat(filepath.Join(sess.Dir, segName)); statErr != nil {
		sess.EnsureSegment(idx)
	}
}

// ServeSegment waits for the requested .ts segment (up to 30s) and streams it.
func ServeSegment(c *gin.Context, sess *transcode.HLSSession, segName string) {
	path, err := sess.WaitForSegment(segName, 30*time.Second)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Header("Content-Type", "video/mp2t")
	c.Header("Cache-Control", "max-age=3600")
	c.File(path)
}
