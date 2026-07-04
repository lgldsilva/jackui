package local

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/localcache"
)

// forceRemoteFS overrides the remote-mount detector for one test and restores
// it afterwards, so we can exercise both the local-disk and rclone branches
// without a real FUSE/NAS mount.
func forceRemoteFS(t *testing.T, remote bool) {
	t.Helper()
	prev := isRemoteFS
	isRemoteFS = func(string) bool { return remote }
	t.Cleanup(func() { isRemoteFS = prev })
}

func decodeCacheStatus(t *testing.T, w *httptest.ResponseRecorder) cacheStatusResponse {
	t.Helper()
	var resp cacheStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, w.Body.String())
	}
	return resp
}

func TestLocalCacheStatus_CacheableTrueOnRemoteMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := cacheBrowser(t)
	if err := os.WriteFile(filepath.Join(dir, "m.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cache, _ := localcache.New(t.TempDir(), 1)
	defer cache.Close()
	forceRemoteFS(t, true)

	r := gin.New()
	r.GET("/s", LocalCacheStatus(b, cache))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/s?mount=Test&path=m.mkv", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	if resp := decodeCacheStatus(t, w); !resp.Cacheable {
		t.Fatalf("cacheable=false on a remote mount; want true")
	}
}

func TestLocalCacheStatus_CacheableFalseOnLocalDisk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := cacheBrowser(t)
	if err := os.WriteFile(filepath.Join(dir, "m.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cache, _ := localcache.New(t.TempDir(), 1)
	defer cache.Close()
	forceRemoteFS(t, false)

	r := gin.New()
	r.GET("/s", LocalCacheStatus(b, cache))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/s?mount=Test&path=m.mkv", nil))
	if resp := decodeCacheStatus(t, w); resp.Cacheable {
		t.Fatalf("cacheable=true on local disk; want false")
	}
}

func TestLocalCacheStatus_CacheableFalseWhenCacheDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := cacheBrowser(t)
	forceRemoteFS(t, true) // even on a remote mount, no cache → not cacheable

	r := gin.New()
	r.GET("/s", LocalCacheStatus(b, nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/s?mount=Test&path=m.mkv", nil))
	if resp := decodeCacheStatus(t, w); resp.Cacheable {
		t.Fatalf("cacheable=true with cache disabled; want false")
	}
}

func TestMountCacheable(t *testing.T) {
	b, dir := cacheBrowser(t)
	if err := os.WriteFile(filepath.Join(dir, "m.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cache, _ := localcache.New(t.TempDir(), 1)
	defer cache.Close()

	// Nil cache → never cacheable.
	if mountCacheable(b, nil, "Test", "m.mkv") {
		t.Fatal("nil cache should not be cacheable")
	}
	// Unresolvable path → false (no panic).
	forceRemoteFS(t, true)
	if mountCacheable(b, cache, "Test", "../escape") {
		t.Fatal("path traversal should not resolve / be cacheable")
	}
	// Remote mount + real file → true.
	if !mountCacheable(b, cache, "Test", "m.mkv") {
		t.Fatal("remote mount with a real file should be cacheable")
	}
}
