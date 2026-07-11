package local

import (
	"context"
	// #nosec G505 -- import de sha1 p/ hash de conteudo (dedup/oshash), nao cripto de seguranca
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
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	lb "github.com/lgldsilva/jackui/internal/local"
)

// localVideoExts mirrors the frontend's video detection — only these get a
// frame preview (ffmpeg on a non-video would just fail/waste work).
var localVideoExts = map[string]bool{
	".mp4": true, ".m4v": true, ".mkv": true, ".avi": true, ".mov": true, ".wmv": true,
	".webm": true, ".flv": true, ".mpeg": true, ".mpg": true, ".ts": true, ".m2ts": true,
}

func LocalThumb(b *lb.Browser) gin.HandlerFunc {
	return func(c *gin.Context) { localThumbHandler(b, c) }
}

func localThumbHandler(b *lb.Browser, c *gin.Context) {
	mount := c.Query("mount")
	path := c.Query("path")
	if mount == "" || path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
		return
	}
	if !CheckMountAccess(b, c, mount) {
		return
	}
	if !localVideoExts[strings.ToLower(filepath.Ext(path))] {
		c.Status(http.StatusNoContent)
		return
	}
	abs, err := resolveLocalAbs(b, mount, ScopePath(b, c, mount, path))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if abs == "" {
		c.Status(http.StatusNotFound)
		return
	}
	at := parseAt(c)
	cacheDir := localThumbCacheDir
	cachePath := thumbCachePath(cacheDir, abs, at)
	if serveCachedThumb(c, cachePath) {
		return
	}
	// A previous attempt failed (e.g. a 4K HDR HEVC frame ffmpeg can't decode
	// quickly): don't re-spend ~12s on every listing — serve 204 until the
	// marker expires. Cleared automatically once a thumb succeeds.
	if negativeThumbFresh(cachePath) {
		c.Status(http.StatusNoContent)
		return
	}
	out := captureThumb(c, abs, at, cacheDir, cachePath)
	if len(out) == 0 {
		c.Status(http.StatusNoContent)
		return
	}
	c.Header(httpshared.CacheControl, httpshared.CachePublicDay)
	c.Data(http.StatusOK, httpshared.MIMEJPEG, out)
}

func resolveLocalAbs(b *lb.Browser, mount, path string) (string, error) {
	abs, err := b.ResolvePath(mount, path)
	if err != nil {
		return "", err
	}
	stat, err := os.Stat(abs)
	if err != nil || stat.IsDir() {
		return "", nil
	}
	return abs, nil
}

func parseAt(c *gin.Context) int {
	at := 10
	if v, e := strconv.Atoi(c.Query("at")); e == nil && v >= 0 {
		at = v
	}
	return at
}

// localThumbSem caps how many local-thumbnail ffmpeg jobs run at once. Listing
// a folder full of 4K HDR HEVC files would otherwise spawn one heavy ffmpeg per
// file simultaneously and peg the (often ARM) host — the symptom that froze the
// UI. 2 keeps things moving without serializing fully. Mirrors the health
// probe's semaphore pattern (internal/streamer/health.go).
var localThumbSem = make(chan struct{}, 2)

// localThumbCacheDir is where generated thumbnails AND negative-result markers
// live. Defaults to a temp dir (fine for tests); main() repoints it at the
// persistent stream DataDir via SetLocalThumbCacheDir so thumbs survive
// restarts instead of being regenerated every boot.
var localThumbCacheDir = filepath.Join(os.TempDir(), "jackui-local-thumbs")

const (
	localThumbTimeout = 12 * time.Second
	// negativeThumbTTL bounds how long a failed-capture marker suppresses
	// retries. Long enough to avoid hammering ffmpeg on every listing of a file
	// it can't decode; short enough that a transient failure self-heals.
	negativeThumbTTL = 24 * time.Hour
)

// SetLocalThumbCacheDir points the local-thumbnail cache at a persistent
// directory. Called once at startup; no-op on empty input.
func SetLocalThumbCacheDir(dir string) {
	if dir != "" {
		localThumbCacheDir = dir
	}
}

func thumbCachePath(cacheDir, abs string, at int) string {
	stat, _ := os.Stat(abs)
	// #nosec G401 -- sha1/md5 p/ hash de conteudo (dedup/oshash), nao uso criptografico de seguranca
	key := fmt.Sprintf("%x", sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", abs, stat.ModTime().UnixNano(), at))))
	return filepath.Join(cacheDir, key+".jpg")
}

// negativeMarkerPath is the sibling marker recording a failed capture for a
// given cache key (so we don't retry a doomed ffmpeg on every listing).
func negativeMarkerPath(cachePath string) string { return cachePath + ".empty" }

// negativeThumbFresh reports whether a recent failed-capture marker exists,
// meaning we should short-circuit to 204 instead of re-running ffmpeg.
func negativeThumbFresh(cachePath string) bool {
	st, err := os.Stat(negativeMarkerPath(cachePath))
	if err != nil {
		return false
	}
	return time.Since(st.ModTime()) < negativeThumbTTL
}

func serveCachedThumb(c *gin.Context, cachePath string) bool {
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return false
	}
	c.Header(httpshared.CacheControl, httpshared.CachePublicDay)
	c.Data(http.StatusOK, httpshared.MIMEJPEG, data)
	return true
}

func captureThumb(c *gin.Context, abs string, at int, cacheDir, cachePath string) []byte {
	ctx, cancel := context.WithTimeout(c.Request.Context(), localThumbTimeout)
	defer cancel()
	// Limit concurrent ffmpeg jobs; if the client navigates away before a slot
	// frees up, bail cheaply rather than piling on more heavy 4K decodes.
	select {
	case localThumbSem <- struct{}{}:
		defer func() { <-localThumbSem }()
	case <-ctx.Done():
		return nil
	}
	seeks := []int{at}
	if at != 1 {
		seeks = append(seeks, 1)
	}
	var out []byte
	for _, s := range seeks {
		// #nosec G204 -- binario fixo/de config; valores de usuario sao operandos de -i ou inteiros; exec sem shell
		cmd := exec.CommandContext(ctx, ffBinary,
			ffHideBanner, ffLogLevel, "error",
			"-ss", strconv.Itoa(s),
			"-i", abs,
			"-frames:v", "1",
			"-vf", "scale=320:-2",
			"-q:v", "5",
			"-f", "mjpeg",
			"-y", pipe1,
		)
		if data, cerr := cmd.Output(); cerr == nil && len(data) > 0 {
			out = data
			break
		}
	}
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	if os.MkdirAll(cacheDir, 0o755) != nil {
		return out
	}
	if len(out) > 0 {
		// #nosec G306 -- arquivo de midia/cache; 0644 intencional p/ leitura
		_ = os.WriteFile(cachePath, out, 0o644)
		_ = os.Remove(negativeMarkerPath(cachePath)) // a success clears any stale failure
		return out
	}
	// Persist the FAILURE so repeated listings don't re-run a ~12s decode for a
	// frame ffmpeg can't produce — but only on a real ffmpeg error/timeout, not
	// when the client cancelled (navigated away) before we finished.
	if ctx.Err() != context.Canceled {
		// #nosec G306 -- arquivo de midia/cache; 0644 intencional p/ leitura
		_ = os.WriteFile(negativeMarkerPath(cachePath), nil, 0o644)
	}
	return out
}
