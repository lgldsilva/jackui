package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// burnSubFileIDPattern follows the fileIDPattern idiom of internal/subtitles
// (opensubtitles.go): the burnsub query param carries an OpenSubtitles FILE ID
// — numeric only — so the value can never smuggle path separators into the
// cache-path join below.
var burnSubFileIDPattern = regexp.MustCompile(`^[0-9]+$`)

// TranscodeCapabilities handles GET /api/transcode/capabilities — returns the cached
// or freshly-probed encoder/decoder matrix. ?refresh=1 forces re-detection (e.g. after GPU upgrade).
func TranscodeCapabilities(c *gin.Context) {
	force := c.Query("refresh") == "1" || c.Query("refresh") == "true"
	caps, err := transcode.Probe(c.Request.Context(), force)
	if err != nil {
		httpshared.RespondError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, caps)
}

// TranscodeStream handles GET /api/stream/transcode/:hash/:file?audio=N&video=h264&burn=N
// [&burnsub=<osFileID>] — burnsub burns an EXTERNAL subtitle previously cached
// by the OpenSubtitles client (resolved by numeric file ID under
// subtitleCacheRoot; the client never sends a path).
// Pipes the torrent file through ffmpeg with chosen options and streams the result.
// Note: no Range support — browsers can't seek transcoded streams.
func TranscodeStream(s *streamer.Streamer, store *downloads.Store, subtitleCacheRoot string) gin.HandlerFunc {
	return func(c *gin.Context) {
		transcodeStreamHandler(c, s, store, subtitleCacheRoot)
	}
}

func transcodeStreamHandler(c *gin.Context, s *streamer.Streamer, store *downloads.Store, subtitleCacheRoot string) {
	var h metainfo.Hash
	if err := h.FromHexString(c.Param("hash")); err != nil {
		httpshared.RespondError(c, http.StatusBadRequest, err)
		return
	}
	fileIdx, err := strconv.Atoi(c.Param("file"))
	if err != nil {
		httpshared.RespondErrorMessage(c, http.StatusBadRequest, errInvalidFileIndex)
		return
	}

	opts := transcode.Options{
		AudioTrack:   httpshared.ParseIntOr(c.Query("audio"), -1),
		SubBurnTrack: httpshared.ParseIntOr(c.Query("burn"), -1),
		VideoCodec:   c.Query("video"),
		AudioCodec:   c.Query("acodec"),
		Container:    c.DefaultQuery("container", "mp4"),
	}

	// External subtitle burn-in (additive; absent/empty burnsub = unchanged
	// behavior). The path is fully guarded before any ffmpeg args are built.
	if burnSubID := c.Query("burnsub"); burnSubID != "" {
		path, status, err := resolveBurnSubFile(subtitleCacheRoot, burnSubID)
		if err != nil {
			httpshared.RespondErrorMessage(c, status, err.Error())
			return
		}
		opts.SubBurnFile = path
		// Typed degradation: an ffmpeg build without libass cannot burn an
		// external subtitle. Reply 400 BEFORE streaming so the web player can
		// fall back to the sidecar <track> path. (Run re-checks as the last
		// line of defense before exec; a nil cache means "not probed yet",
		// which Run reports as today.)
		if caps := transcode.Cached(); caps != nil && !caps.HasSubtitlesFilter {
			httpshared.RespondError(c, http.StatusBadRequest, transcode.ErrSubtitlesFilterUnavailable)
			return
		}
	}

	userID, _, _ := auth.UserIDFromCtx(c)
	if tryServeFromCompleted(c, store, h.HexString(), fileIdx, opts, s.FileRelPath(h, fileIdx), userID) {
		return
	}

	reader, _, err := s.FileReader(h, fileIdx)
	if err != nil {
		httpshared.RespondError(c, http.StatusNotFound, err)
		return
	}
	defer reader.Close()

	if err := transcode.Run(c.Request.Context(), reader, c.Writer, opts); err != nil {
		c.Abort()
		// Run's last line of defense can still surface the libass sentinel
		// (probe completing after the handler's Cached() check): client-input
		// problem → typed 400, same as the early check — never a 500.
		httpshared.RespondError(c, transcodeRunStatus(err), err)
	}
}

// transcodeRunStatus maps a transcode.Run failure to its HTTP status: the
// libass sentinel is the same client-input problem the early Cached() check
// reports as 400 — errors.Is also matches ValidateOptions' wrapped form.
func transcodeRunStatus(err error) int {
	if errors.Is(err, transcode.ErrSubtitlesFilterUnavailable) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// resolveBurnSubFile turns a burnsub=<osFileID> query param into the cached
// VTT path for burn-in, applying every guard before any ffmpeg work:
//
//  1. cache enabled (empty root = OpenSubtitles caching disabled → 400,
//     never a silent fallback to burning nothing);
//  2. numeric file ID (burnSubFileIDPattern — subtitles fileIDPattern idiom);
//  3. containment under the cache root (transcode.ValidBurnFilePath);
//  4. the file actually exists on disk ("subtitle not cached", 404-style 400
//     — we never download here, burn-in is a cache-hit-only path).
//
// The returned status is the HTTP code the handler must use when err != nil.
func resolveBurnSubFile(cacheRoot, fileID string) (string, int, error) {
	switch {
	case cacheRoot == "":
		return "", http.StatusBadRequest, errors.New("subtitle cache disabled")
	case !burnSubFileIDPattern.MatchString(fileID):
		return "", http.StatusBadRequest, fmt.Errorf("invalid burnsub file id %q", fileID)
	}
	// Same layout as internal/subtitles cachePath (fileID + ".vtt").
	path := filepath.Join(cacheRoot, fileID+".vtt")
	if !transcode.ValidBurnFilePath(cacheRoot, path) {
		return "", http.StatusBadRequest, fmt.Errorf("invalid subtitle path for file id %q", fileID)
	}
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
		return "", http.StatusBadRequest, fmt.Errorf("subtitle not cached: file id %s — download it first", fileID)
	}
	return path, 0, nil
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
		c.Abort()
		httpshared.RespondError(c, transcodeRunStatus(err), err)
	}
	return true
}

type GPUInfo struct {
	Type      string `json:"type"`      // "nvidia" | "vaapi" | "none"
	GPU       int    `json:"gpu"`       // percentage, e.g. 15
	VRAMUsed  int    `json:"vramUsed"`  // MB
	VRAMTotal int    `json:"vramTotal"` // MB
}

// nvidiaSMIDirs are the only directories nvidia-smi is looked up in — fixed,
// root-owned locations (the nvidia-container-toolkit injects the binary at
// /usr/bin/nvidia-smi) instead of a PATH walk that a writable entry could
// hijack. Var so tests can point it at a stub directory.
var nvidiaSMIDirs = []string{"/usr/bin", "/usr/local/bin"}

// nvidiaSMIPath returns the first regular, executable nvidia-smi found in
// nvidiaSMIDirs, or "" when the host has none (no NVIDIA runtime).
func nvidiaSMIPath() string {
	for _, dir := range nvidiaSMIDirs {
		p := filepath.Join(dir, "nvidia-smi")
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
			return p
		}
	}
	return ""
}

func getGPUStats() *GPUInfo {
	// 1. Try NVIDIA. Bounded: nvidia-smi can hang on a wedged driver, and an
	// unbounded exec stalled the /api/transcode/active poll for as long as the
	// tool took to return. On timeout the process is killed and we fall through.
	if info := nvidiaGPUStats(); info != nil {
		return info
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

// nvidiaGPUStats queries nvidia-smi (bounded to 5s) and returns nil when the
// binary is absent, fails, times out or prints an unexpected row.
func nvidiaGPUStats() *GPUInfo {
	smi := nvidiaSMIPath()
	if smi == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, smi, "--query-gpu=utilization.gpu,memory.used,memory.total", "--format=csv,noheader,nounits")
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
	return nil
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
			httpshared.RespondErrorMessage(c, http.StatusNotFound, "HLS manager not active")
			return
		}
		key := c.Param("key")
		if key == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "missing session key")
			return
		}
		hlsMgr.Close(key)
		c.Status(http.StatusNoContent)
	}
}
