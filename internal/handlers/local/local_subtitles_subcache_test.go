package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/localcache"
)

func statTempFile(t *testing.T, dir, name string, data []byte) (string, os.FileInfo) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", name, err)
	}
	return p, st
}

func TestLocalSubVTTPath(t *testing.T) {
	// nil cache or nil stat → empty (no cache configured).
	if got := localSubVTTPath(nil, "/a.mkv", nil, 3); got != "" {
		t.Fatalf("nil cache → want empty, got %q", got)
	}
	cache, err := localcache.New(t.TempDir(), 1)
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	defer cache.Close()
	abs, st := statTempFile(t, t.TempDir(), "movie.mkv", []byte("x"))

	if got := localSubVTTPath(cache, abs, nil, 3); got != "" {
		t.Fatalf("nil stat → want empty, got %q", got)
	}
	p3 := localSubVTTPath(cache, abs, st, 3)
	if filepath.Dir(p3) != filepath.Join(cache.Root(), "subs") {
		t.Fatalf("want path under <root>/subs, got %q", p3)
	}
	if localSubVTTPath(cache, abs, st, 3) != p3 {
		t.Fatal("must be deterministic for the same inputs")
	}
	if localSubVTTPath(cache, abs, st, 4) == p3 {
		t.Fatal("a different track must yield a different cache path")
	}
}

func TestPersistVTT(t *testing.T) {
	persistVTT("", []byte("x")) // no-op, must not panic

	target := filepath.Join(t.TempDir(), "subs", "x.vtt")
	persistVTT(target, []byte("WEBVTT\n\nhi"))
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "WEBVTT\n\nhi" {
		t.Fatalf("content = %q", got)
	}
	// The temp file must have been renamed away (atomic publish).
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Error(".tmp leftover — rename didn't happen")
	}
}

func TestServeCachedVTT(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// empty path → not handled.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	if serveCachedVTT(c, "") {
		t.Fatal("empty path must return false")
	}
	// missing file → not handled.
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	if serveCachedVTT(c, filepath.Join(t.TempDir(), "nope.vtt")) {
		t.Fatal("missing file must return false")
	}
	// existing file → served with 200 + body.
	p, _ := statTempFile(t, t.TempDir(), "c.vtt", []byte("WEBVTT\n\nok"))
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	if !serveCachedVTT(c, p) {
		t.Fatal("existing file must return true")
	}
	if w.Code != http.StatusOK || w.Body.String() != "WEBVTT\n\nok" {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
}

// subExtractRouter wires LocalSubtitleExtract over a mount rooted at a temp dir
// with one media file, plus a cache.
func subExtractRouter(t *testing.T) (*gin.Engine, *localcache.Cache, string, os.FileInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	abs, st := statTempFile(t, mountDir, "movie.mkv", []byte("not-real-media"))
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: mountDir}})
	cache, err := localcache.New(t.TempDir(), 1)
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	r := gin.New()
	r.GET("/sub", LocalSubtitleExtract(b, cache))
	return r, cache, abs, st
}

// Cache hit: a previously-extracted VTT on disk is served instantly (no ffmpeg).
func TestLocalSubtitleExtract_CacheHit(t *testing.T) {
	r, cache, abs, st := subExtractRouter(t)
	defer cache.Close()
	persistVTT(localSubVTTPath(cache, abs, st, 3), []byte("WEBVTT\n\ncached"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/sub?mount=M&path=movie.mkv&track=3", nil))
	if w.Code != http.StatusOK || w.Body.String() != "WEBVTT\n\ncached" {
		t.Fatalf("cache hit → want 200 cached VTT, got %d %q", w.Code, w.Body.String())
	}
}

// No cache copy + slow mount → background extraction kicks off and the request
// returns 503 {code:"extracting"} instead of blocking.
func TestLocalSubtitleExtract_BackgroundExtracting(t *testing.T) {
	r, cache, abs, st := subExtractRouter(t)
	defer cache.Close()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/sub?mount=M&path=movie.mkv&track=3", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("uncached source → want 503, got %d %q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "extracting") {
		t.Errorf("503 body should carry code=extracting, got %s", w.Body.String())
	}

	// Aguarda o job de extração em background terminar para liberar o arquivo no TempDir
	vttPath := localSubVTTPath(cache, abs, st, 3)
	for i := 0; i < 300; i++ {
		if _, extracting := subExtractJobs.Load(vttPath); !extracting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// extractEmbeddedVTT must surface an error for a non-media input (covers the
// ffmpeg error path). Skipped where ffmpeg isn't installed.
func TestExtractEmbeddedVTT_Error(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	_, err := extractEmbeddedVTT(context.Background(), filepath.Join(t.TempDir(), "movie.mkv"), 0)
	if err == nil {
		t.Fatal("expected an ffmpeg error for a missing/non-media file")
	}
}

// extractAndServe must answer 502 when ffmpeg fails. Skipped without ffmpeg.
func TestExtractAndServe_ErrorIs502(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	gin.SetMode(gin.TestMode)
	abs, _ := statTempFile(t, t.TempDir(), "bad.mkv", []byte("not-media"))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	extractAndServe(c, abs, 0, 10*time.Second, "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("ffmpeg failure → want 502, got %d %q", w.Code, w.Body.String())
	}
}
