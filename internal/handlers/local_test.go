package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/local"
)

func TestLocalDeleteAndPromote(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create temporary directories
	meusDownloadsDir := t.TempDir()
	downloadsSharedDir := t.TempDir()
	sharedTargetDir := t.TempDir() // Shared destination (JACKUI_SHARED_DIR)

	// Create test file in Meus Downloads
	testFileMeus := filepath.Join(meusDownloadsDir, "test.mp4")
	if err := os.WriteFile(testFileMeus, []byte("movie content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create test file in Shared Downloads
	testFileShared := filepath.Join(downloadsSharedDir, "shared.mp4")
	if err := os.WriteFile(testFileShared, []byte("shared content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Meus downloads", Path: meusDownloadsDir},
		{Name: "Downloads", Path: downloadsSharedDir},
	})

	router := gin.New()
	router.DELETE("/api/local/file", LocalDelete(b, nil, nil))
	router.POST("/api/local/promote", LocalPromote(b, nil, nil, sharedTargetDir, nil, nil, nil))

	// 1. DELETE - Attempt to delete from general 'Downloads' should be rejected (403 Forbidden)
	{
		req := httptest.NewRequest("DELETE", "/api/local/file?mount=Downloads&path=shared.mp4", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("DELETE non-modifiable mount: status = %d, want 403", w.Code)
		}
		if _, err := os.Stat(testFileShared); os.IsNotExist(err) {
			t.Error("file in general Downloads was unexpectedly deleted")
		}
	}

	// 2. DELETE - Attempt to delete mount root should be rejected
	{
		req := httptest.NewRequest("DELETE", "/api/local/file?mount=Meus+downloads&path=.", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("DELETE mount root: status = %d, want 400", w.Code)
		}
	}

	// 3. DELETE - Successful deletion from 'Meus downloads'
	{
		req := httptest.NewRequest("DELETE", "/api/local/file?mount=Meus+downloads&path=test.mp4", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("DELETE test.mp4: status = %d, want 200", w.Code)
		}

		if _, err := os.Stat(testFileMeus); !os.IsNotExist(err) {
			t.Error("file in Meus downloads was not deleted")
		}
	}

	// Re-create test file in Meus Downloads for promote tests
	if err := os.WriteFile(testFileMeus, []byte("movie content"), 0644); err != nil {
		t.Fatalf("failed to re-create test file: %v", err)
	}

	// 4. PROMOTE - Attempt to promote from general 'Downloads' should be rejected (403 Forbidden)
	{
		body := localPromoteReq{
			Mount:        "Downloads",
			Path:         "shared.mp4",
			TargetSubdir: "filmes",
		}
		bJSON, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/local/promote", bytes.NewReader(bJSON))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("PROMOTE non-modifiable mount: status = %d, want 403", w.Code)
		}
	}

	// 5. PROMOTE - Successful promote from 'Meus downloads'
	{
		body := localPromoteReq{
			Mount:        "Meus downloads",
			Path:         "test.mp4",
			TargetSubdir: "filmes",
		}
		bJSON, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/local/promote", bytes.NewReader(bJSON))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("PROMOTE test.mp4: status = %d, want 200. Body: %s", w.Code, w.Body.String())
		}

		// Verify moved file
		movedPath := filepath.Join(sharedTargetDir, "filmes", "test.mp4")
		if _, err := os.Stat(movedPath); os.IsNotExist(err) {
			t.Errorf("promoted file not found at expected destination: %s", movedPath)
		}

		// Verify source file is gone
		if _, err := os.Stat(testFileMeus); !os.IsNotExist(err) {
			t.Error("source file in Meus downloads was not deleted after promotion")
		}
	}
}

func TestScopeUser_AdminTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/?user=targetuser", nil)
	setAuth(c, 1, true)

	got := scopeUser(c)
	if got != "targetuser" {
		t.Errorf("got %q, want 'targetuser'", got)
	}
}

func TestScopeUser_NonAdminTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/?user=targetuser", nil)

	got := scopeUser(c)
	if got != "" {
		t.Errorf("expected empty for unauthenticated, got %q", got)
	}
}

func TestLocalThumb_NonVideoExt204(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	router := gin.New()
	router.GET("/api/local/thumb", LocalThumb(b))

	req := httptest.NewRequest("GET", "/api/local/thumb?mount=Test&path=doc.txt", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
}

func TestLocalTranscode_UnknownMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Real", Path: mountDir},
	})

	router := gin.New()
	router.GET("/api/local/transcode", LocalTranscode(b))

	req := httptest.NewRequest("GET", "/api/local/transcode?mount=DoesNotExist&path=test.mp4", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestLocalWalk_UnknownMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Real", Path: mountDir},
	})

	router := gin.New()
	router.GET("/api/local/walk", LocalWalk(b))

	req := httptest.NewRequest("GET", "/api/local/walk?mount=DoesNotExist&path=test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestLocalList_WalkErr(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	router := gin.New()
	router.GET("/api/local/list", LocalList(b, nil))

	req := httptest.NewRequest("GET", "/api/local/list?mount=Test&path=../escape", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestExecPromoteMoves_EmptyPaths(t *testing.T) {
	b := local.NewBrowser(nil)
	moved, errs := execPromoteMoves(b, nil, "Test", nil, "/target")
	if moved != 0 || len(errs) != 0 {
		t.Errorf("expected 0 moves, got moved=%d errs=%d", moved, len(errs))
	}
}

func TestServeCachedThumb_Missing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	if serveCachedThumb(c, "/nonexistent/cache.jpg") {
		t.Error("expected false for missing cache")
	}
}

func TestServeCachedThumb_Hit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "thumb.jpg")
	os.WriteFile(cachePath, []byte{0xff, 0xd8, 0xff}, 0644)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	if !serveCachedThumb(c, cachePath) {
		t.Fatal("expected true for existing cache")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("Content-Type = %q", w.Header().Get("Content-Type"))
	}
}
