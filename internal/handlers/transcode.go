package handlers

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// TranscodeCapabilities handles GET /api/transcode/capabilities — returns the cached
// or freshly-probed encoder/decoder matrix. ?refresh=1 forces re-detection (e.g. after GPU upgrade).
func TranscodeCapabilities(c *gin.Context) {
	force := c.Query("refresh") == "1" || c.Query("refresh") == "true"
	caps, err := transcode.Probe(c.Request.Context(), force)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, caps)
}

// TranscodeStream handles GET /api/stream/transcode/:hash/:file?audio=N&video=h264&burn=N
// Pipes the torrent file through ffmpeg with chosen options and streams the result.
// Note: no Range support — browsers can't seek transcoded streams.
func TranscodeStream(s *streamer.Streamer, store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		transcodeStreamHandler(c, s, store)
	}
}

func transcodeStreamHandler(c *gin.Context, s *streamer.Streamer, store *downloads.Store) {
	var h metainfo.Hash
	if err := h.FromHexString(c.Param("hash")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	fileIdx, err := strconv.Atoi(c.Param("file"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
		return
	}

	opts := transcode.Options{
		AudioTrack:   httpshared.ParseIntOr(c.Query("audio"), -1),
		SubBurnTrack: httpshared.ParseIntOr(c.Query("burn"), -1),
		VideoCodec:   c.Query("video"),
		AudioCodec:   c.Query("acodec"),
		Container:    c.DefaultQuery("container", "mp4"),
	}

	userID, _, _ := auth.UserIDFromCtx(c)
	if tryServeFromCompleted(c, store, h.HexString(), fileIdx, opts, s.FileRelPath(h, fileIdx), userID) {
		return
	}

	reader, _, err := s.FileReader(h, fileIdx)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer reader.Close()

	if err := transcode.Run(c.Request.Context(), reader, c.Writer, opts); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// tryServeFromCompleted transcodes a finished download straight from disk.
// relPath (the file's torrent-relative path from the cached metainfo) lets the
// store resolve files inside whole-torrent rows, whose file_path is the
// torrent's destination directory.
func tryServeFromCompleted(c *gin.Context, store *downloads.Store, hashHex string, fileIdx int, opts transcode.Options, relPath string, userID int) bool {
	if store == nil {
		return false
	}
	path, err := store.GetCompletedPathRel(hashHex, fileIdx, relPath, userID)
	if err != nil || path == "" {
		return false
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return false
	}
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := transcode.Run(c.Request.Context(), f, c.Writer, opts); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
	return true
}

type GPUInfo struct {
	Type      string `json:"type"`      // "nvidia" | "vaapi" | "none"
	GPU       int    `json:"gpu"`       // percentage, e.g. 15
	VRAMUsed  int    `json:"vramUsed"`  // MB
	VRAMTotal int    `json:"vramTotal"` // MB
}

func getGPUStats() *GPUInfo {
	// 1. Try NVIDIA
	cmd := exec.Command("nvidia-smi", "--query-gpu=utilization.gpu,memory.used,memory.total", "--format=csv,noheader,nounits")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil {
		parts := strings.Split(strings.TrimSpace(out.String()), ",")
		if len(parts) >= 3 {
			gpu, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
			used, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
			total, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
			return &GPUInfo{
				Type:      "nvidia",
				GPU:       gpu,
				VRAMUsed:  used,
				VRAMTotal: total,
			}
		}
	}

	// 2. Try VAAPI /sys/class/drm/card0/device/gpu_busy_percent (Intel/AMD)
	if bytesRead, err := os.ReadFile("/sys/class/drm/card0/device/gpu_busy_percent"); err == nil {
		if val, err := strconv.Atoi(strings.TrimSpace(string(bytesRead))); err == nil {
			return &GPUInfo{
				Type: "vaapi",
				GPU:  val,
			}
		}
	}

	return &GPUInfo{Type: "none"}
}

// TranscodeActive handles GET /api/transcode/active
func TranscodeActive(hlsMgr *transcode.HLSSessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if hlsMgr == nil {
			c.JSON(http.StatusOK, gin.H{"sessions": []interface{}{}, "gpu": getGPUStats()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"sessions": hlsMgr.Sessions(),
			"gpu":      getGPUStats(),
		})
	}
}

// TranscodeKill handles DELETE /api/transcode/active/:key
func TranscodeKill(hlsMgr *transcode.HLSSessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if hlsMgr == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "HLS manager não ativo"})
			return
		}
		key := c.Param("key")
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing session key"})
			return
		}
		hlsMgr.Close(key)
		c.Status(http.StatusNoContent)
	}
}
