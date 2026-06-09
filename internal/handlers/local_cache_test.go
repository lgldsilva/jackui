package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/localcache"
)

func cacheBrowser(t *testing.T) (*local.Browser, string) {
	t.Helper()
	dir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{{Name: "Test", Path: dir}})
	return b, dir
}

func TestLocalCacheStart_EnqueuesAndServesCached(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := cacheBrowser(t)
	if err := os.WriteFile(filepath.Join(dir, "movie.mkv"), []byte("the movie bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cache, err := localcache.New(t.TempDir(), 1)
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	defer cache.Close()

	r := gin.New()
	r.POST("/c", LocalCacheStart(b, cache))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/c?mount=Test&path=movie.mkv", nil))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202; body=%s", w.Code, w.Body.String())
	}

	// Worker copies async — poll until ready, then the cached path must exist.
	ready := false
	for i := 0; i < 100; i++ {
		if cache.StatusFor("Test", "movie.mkv").Status == "ready" {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("file did not finish caching")
	}
	if _, ok := cache.CachedPath("Test", "movie.mkv"); !ok {
		t.Fatal("expected a cached copy")
	}
}

func TestLocalCacheStart_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := cacheBrowser(t)
	r := gin.New()
	r.POST("/c", LocalCacheStart(b, nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/c?mount=Test", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func TestLocalCacheStart_NilCacheUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := cacheBrowser(t)
	_ = os.WriteFile(filepath.Join(dir, "m.mkv"), []byte("x"), 0o644)
	r := gin.New()
	r.POST("/c", LocalCacheStart(b, nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/c?mount=Test&path=m.mkv", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 (cache disabled)", w.Code)
	}
}

func TestLocalCacheStatus_NoneAndNilCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := cacheBrowser(t)
	cache, _ := localcache.New(t.TempDir(), 1)
	defer cache.Close()

	r := gin.New()
	r.GET("/s", LocalCacheStatus(b, cache))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/s?mount=Test&path=never.mkv", nil))
	var snap localcache.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if snap.Status != "none" {
		t.Fatalf("status=%q want none", snap.Status)
	}

	// Nil cache → still 200 / none (caching disabled gracefully).
	r2 := gin.New()
	r2.GET("/s", LocalCacheStatus(b, nil))
	w2 := httptest.NewRecorder()
	r2.ServeHTTP(w2, httptest.NewRequest("GET", "/s?mount=Test&path=x.mkv", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("nil-cache status=%d want 200", w2.Code)
	}
}

func TestLocalCacheDelete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := cacheBrowser(t)
	r := gin.New()
	r.DELETE("/c", LocalCacheDelete(b, nil)) // nil cache → no-op, still 200
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/c?mount=Test&path=x.mkv", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
}

func TestCachedAbsAndCacheReady(t *testing.T) {
	cache, _ := localcache.New(t.TempDir(), 1)
	defer cache.Close()
	// Nil cache → fallback unchanged; no cacheReady.
	if got := cachedAbs(nil, "M", "p", "/orig"); got != "/orig" {
		t.Fatalf("cachedAbs(nil)=%q want /orig", got)
	}
	if _, ok := cacheReady(nil, "M", "p"); ok {
		t.Fatal("cacheReady(nil) should be false")
	}
	// Absent entry → fallback.
	if got := cachedAbs(cache, "M", "p", "/orig"); got != "/orig" {
		t.Fatalf("cachedAbs(absent)=%q want /orig", got)
	}
}
