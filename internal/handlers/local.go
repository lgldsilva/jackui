package handlers

import (
	"context"
	"crypto/sha1"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/local"
	"github.com/luizg/jackui/internal/transcode"
)

// LocalMounts handles GET /api/local/mounts -> []Mount
func LocalMounts(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, b.Mounts())
	}
}

// LocalList handles GET /api/local/list?mount=NAME&path=REL -> []Entry
func LocalList(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount parameter"})
			return
		}

		entries, err := b.List(mount, path)
		if err != nil {
			if isTraversalErr(err) {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "path not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, entries)
	}
}

// LocalFile handles GET /api/local/file?mount=NAME&path=REL/FILE
// Uses http.ServeFile which handles Range requests natively.
func LocalFile(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount or path parameter"})
			return
		}

		abs, err := b.ResolvePath(mount, path)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		stat, err := os.Stat(abs)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if stat.IsDir() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "path is a directory"})
			return
		}

		http.ServeFile(c.Writer, c.Request, abs)
	}
}

// localVideoExts mirrors the frontend's video detection — only these get a
// frame preview (ffmpeg on a non-video would just fail/waste work).
var localVideoExts = map[string]bool{
	".mp4": true, ".m4v": true, ".mkv": true, ".avi": true, ".mov": true, ".wmv": true,
	".webm": true, ".flv": true, ".mpeg": true, ".mpg": true, ".ts": true, ".m2ts": true,
}

// LocalThumb handles GET /api/local/thumb?mount=NAME&path=REL&at=SECONDS —
// extracts a single early frame from a local video file as JPEG, cached on disk.
// Used by the file browser to show a preview instead of a generic icon. Loaded
// by <img>, so it accepts ?token= (see isMediaPath).
func LocalThumb(b *local.Browser) gin.HandlerFunc {
	cacheDir := filepath.Join(os.TempDir(), "jackui-local-thumbs")
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount or path parameter"})
			return
		}
		if !localVideoExts[strings.ToLower(filepath.Ext(path))] {
			c.Status(http.StatusNoContent)
			return
		}
		abs, err := b.ResolvePath(mount, path)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		stat, err := os.Stat(abs)
		if err != nil || stat.IsDir() {
			c.Status(http.StatusNotFound)
			return
		}
		at := 10
		if v, e := strconv.Atoi(c.Query("at")); e == nil && v >= 0 {
			at = v
		}

		// Cache key includes mod time so editing/replacing the file busts it.
		key := fmt.Sprintf("%x", sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", abs, stat.ModTime().UnixNano(), at))))
		cachePath := filepath.Join(cacheDir, key+".jpg")
		if data, rerr := os.ReadFile(cachePath); rerr == nil {
			c.Header("Cache-Control", "public, max-age=86400")
			c.Data(http.StatusOK, "image/jpeg", data)
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		// Try the requested timestamp first, then 1s — a short clip may have no
		// frame at `at`, and the very start is sometimes black/garbage.
		seeks := []int{at}
		if at != 1 {
			seeks = append(seeks, 1)
		}
		var out []byte
		for _, s := range seeks {
			cmd := exec.CommandContext(ctx, "ffmpeg",
				"-hide_banner", "-loglevel", "error",
				"-ss", strconv.Itoa(s),
				"-i", abs,
				"-frames:v", "1",
				"-vf", "scale=320:-2",
				"-q:v", "5",
				"-f", "mjpeg",
				"-y", "pipe:1",
			)
			if data, cerr := cmd.Output(); cerr == nil && len(data) > 0 {
				out = data
				break
			}
		}
		if len(out) == 0 {
			c.Status(http.StatusNoContent)
			return
		}
		if os.MkdirAll(cacheDir, 0o755) == nil {
			_ = os.WriteFile(cachePath, out, 0o644)
		}
		c.Header("Cache-Control", "public, max-age=86400")
		c.Data(http.StatusOK, "image/jpeg", out)
	}
}

// LocalTranscode handles GET /api/local/transcode?mount=NAME&path=REL
// Transcodes a local file to H.264/AAC fragmented MP4 so browsers can play
// formats like MKV, AVI or any container not natively supported.
func LocalTranscode(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount or path parameter"})
			return
		}
		abs, err := b.ResolvePath(mount, path)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		stat, err := os.Stat(abs)
		if err != nil || stat.IsDir() {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}
		f, err := os.Open(abs)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer f.Close()

		opts := transcode.Options{
			AudioTrack:  -1,
			SubBurnTrack: -1,
			VideoCodec:  "h264",
			AudioCodec:  "aac",
			Container:   "mp4",
		}
		if err := transcode.Run(c.Request.Context(), f, c.Writer, opts); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
	}
}

func isTraversalErr(err error) bool {
	s := err.Error()
	return strings.Contains(s, "traversal") ||
		strings.Contains(s, "must be relative") ||
		strings.Contains(s, "mount") && strings.Contains(s, "not found")
}
