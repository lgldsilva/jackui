package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/streamer"
)

func newDownloadsStoreWithAuth(t *testing.T) (*downloads.Store, *auth.Store) {
	t.Helper()
	dlStore, err := downloads.New(t.TempDir() + "/downloads.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dlStore.Close() })
	authStore, err := auth.New(t.TempDir() + "/auth.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(authStore.Close)
	return dlStore, authStore
}

func TestDownloadsList_WithAuthStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dlStore, _ := newDownloadsStoreWithAuth(t)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/downloads", DownloadsList(dlStore, s, "/downloads"))

	req := httptest.NewRequest("GET", "/api/downloads", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	_ = resp
}

func TestDownloadsListFiltered_NoFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.GET("/api/downloads/filtered", DownloadsListFiltered(store, nil))

	req := httptest.NewRequest("GET", "/api/downloads/filtered", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsCreate_NoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.POST("/api/downloads", DownloadsCreate(store))

	req := httptest.NewRequest("POST", "/api/downloads", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsTrackers_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.GET("/api/downloads/:id/trackers", DownloadsTrackers(store))

	req := httptest.NewRequest("GET", "/api/downloads/999/trackers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsCategories_ReturnsList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.GET("/api/downloads/categories", DownloadsCategories(store))

	req := httptest.NewRequest("GET", "/api/downloads/categories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsPauseAll_ReturnsResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.PATCH("/api/downloads/pause-all", DownloadsPauseAll(store))

	req := httptest.NewRequest("PATCH", "/api/downloads/pause-all", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsResumeAll_ReturnsResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.PATCH("/api/downloads/resume-all", DownloadsResumeAll(store))

	req := httptest.NewRequest("PATCH", "/api/downloads/resume-all", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsBatchPause_NoIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.POST("/api/downloads/batch/pause", DownloadsBatchPause(store))

	body := []byte(`{"ids":[]}`)
	req := httptest.NewRequest("POST", "/api/downloads/batch/pause", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsBatchResume_NoIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.POST("/api/downloads/batch/resume", DownloadsBatchResume(store))

	body := []byte(`{"ids":[]}`)
	req := httptest.NewRequest("POST", "/api/downloads/batch/resume", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsBatchDelete_NoIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.POST("/api/downloads/batch/delete", DownloadsBatchDelete(store))

	body := []byte(`{"ids":[]}`)
	req := httptest.NewRequest("POST", "/api/downloads/batch/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsRecheck_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/downloads/:id/recheck", DownloadsRecheck(store, s))

	req := httptest.NewRequest("POST", "/api/downloads/999/recheck", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsDetails_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/downloads/:id/details", DownloadsDetails(store, s))

	req := httptest.NewRequest("GET", "/api/downloads/999/details", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsUsers_ReturnsList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.GET("/api/downloads/users", DownloadsUsers(store, nil))

	req := httptest.NewRequest("GET", "/api/downloads/users", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestEnrichETA_EmptyHash(t *testing.T) {
	d := &downloads.Download{InfoHash: ""}
	enrichETA(d, nil)
	if d.DownRate != 0 {
		t.Errorf("expected 0 DownRate, got %d", d.DownRate)
	}
}

func TestEnrichETA_NilStreamer(t *testing.T) {
	d := &downloads.Download{InfoHash: "aaa", FileSize: 1000}
	enrichETA(d, nil)
}

func TestEnrichETA_UnknownHash(t *testing.T) {
	s := streamer.NewForTesting()
	d := &downloads.Download{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FileSize: 1000}
	enrichETA(d, s)
}

func TestMarkPromoted_NilList(t *testing.T) {
	markPromoted(nil, "/downloads")
}

func TestMarkPromoted_EmptyList(t *testing.T) {
	markPromoted([]downloads.Download{}, "/downloads")
}

func TestMarkPromoted_CrossDevice(t *testing.T) {
	list := []downloads.Download{
		{FilePath: "/mnt/other/file.mp4", Name: "file.mp4", Status: downloads.StatusCompleted},
	}
	markPromoted(list, "/downloads")
	if !list[0].Promoted {
		t.Error("expected file outside download dir to be marked promoted")
	}
}

func TestMarkPromoted_NotCompleted(t *testing.T) {
	list := []downloads.Download{
		{FilePath: "/mnt/other/file.mp4", Name: "file.mp4", Status: downloads.StatusDownloading},
	}
	markPromoted(list, "/downloads")
	if list[0].Promoted {
		t.Error("expected downloading file to NOT be marked promoted")
	}
}

func TestMarkPromoted_InsideDownloadDir(t *testing.T) {
	list := []downloads.Download{
		{FilePath: "/downloads/file.mp4", Name: "file.mp4", Status: downloads.StatusCompleted},
	}
	markPromoted(list, "/downloads")
	if list[0].Promoted {
		t.Error("expected file inside download dir to NOT be marked promoted")
	}
}
