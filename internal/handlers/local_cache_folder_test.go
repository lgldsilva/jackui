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
	"github.com/lgldsilva/jackui/internal/localcache"
)

// LocalCacheFolder enqueues every playable file under a folder (recursive) on a
// remote mount.
func TestLocalCacheFolder_EnqueuesPlayableRecursive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := cacheBrowser(t)
	// Two playable files (one nested) + one non-media file that must be skipped.
	if err := os.WriteFile(filepath.Join(dir, "ep01.mkv"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "S02"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "S02", "ep02.mp4"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache, _ := localcache.New(t.TempDir(), 1)
	defer cache.Close()
	forceRemoteFS(t, true)

	r := gin.New()
	r.POST("/cf", LocalCacheFolder(b, cache))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/cf?mount=Test&path=", nil))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Queued    int  `json:"queued"`
		Cacheable bool `json:"cacheable"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Cacheable {
		t.Fatal("cacheable=false on a remote mount; want true")
	}
	if resp.Queued != 2 {
		t.Fatalf("queued=%d want 2 (the two media files, txt skipped)", resp.Queued)
	}

	// Espera que as cópias em background terminem para liberar o TempDir
	ready := false
	for i := 0; i < 300; i++ {
		if cache.StatusFor("Test", "ep01.mkv").Status == "ready" &&
			cache.StatusFor("Test", "S02/ep02.mp4").Status == "ready" {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("arquivos do folder não terminaram de ser copiados para o cache")
	}
}

// On a local-disk mount nothing is enqueued (already fast/seekable).
func TestLocalCacheFolder_LocalDiskNoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := cacheBrowser(t)
	_ = os.WriteFile(filepath.Join(dir, "ep01.mkv"), []byte("a"), 0o644)
	cache, _ := localcache.New(t.TempDir(), 1)
	defer cache.Close()
	forceRemoteFS(t, false)

	r := gin.New()
	r.POST("/cf", LocalCacheFolder(b, cache))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/cf?mount=Test&path=", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	var resp struct {
		Queued    int  `json:"queued"`
		Cacheable bool `json:"cacheable"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Cacheable || resp.Queued != 0 {
		t.Fatalf("local disk: queued=%d cacheable=%v; want 0/false", resp.Queued, resp.Cacheable)
	}
}

func TestLocalCacheFolder_MissingMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := cacheBrowser(t)
	r := gin.New()
	r.POST("/cf", LocalCacheFolder(b, nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/cf?path=x", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (missing mount)", w.Code)
	}
}

func TestLocalCacheFolder_NilCacheUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := cacheBrowser(t)
	r := gin.New()
	r.POST("/cf", LocalCacheFolder(b, nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/cf?mount=Test&path=", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 (cache disabled)", w.Code)
	}
}
