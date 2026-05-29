package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/config"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/streamer"
)

func newDownloadsStore(t *testing.T) *downloads.Store {
	t.Helper()
	s, err := downloads.New(t.TempDir() + "/downloads.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestDownloadsList_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.GET("/api/downloads", DownloadsList(store, nil, ""))

	req := httptest.NewRequest("GET", "/api/downloads", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var list []interface{}
	json.Unmarshal(w.Body.Bytes(), &list)
	if list == nil {
		t.Error("expected non-nil empty array")
	}
}

func TestDownloadsCreate_Minimal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.POST("/api/downloads", DownloadsCreate(store))

	body := map[string]interface{}{
		"infoHash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"magnet":   "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"name":     "Test Torrent",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/downloads", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsCreate_MissingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.POST("/api/downloads", DownloadsCreate(store))

	body := map[string]string{
		"name": "test",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/downloads", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsDelete_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.DELETE("/api/downloads/:id", DownloadsDelete(store))

	req := httptest.NewRequest("DELETE", "/api/downloads/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsDelete_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.DELETE("/api/downloads/:id", DownloadsDelete(store))

	req := httptest.NewRequest("DELETE", "/api/downloads/999", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Delete for non-existent returns 500 (store.Delete fails)
	if w.Code != http.StatusNoContent && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 204 or 500; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsPause_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.PATCH("/api/downloads/:id/pause", DownloadsPause(store))

	req := httptest.NewRequest("PATCH", "/api/downloads/notanumber/pause", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsResume_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.PATCH("/api/downloads/:id/resume", DownloadsResume(store))

	req := httptest.NewRequest("PATCH", "/api/downloads/notanumber/resume", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsListFiltered_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.GET("/api/downloads/filtered", DownloadsListFiltered(store, nil))

	req := httptest.NewRequest("GET", "/api/downloads/filtered?status=downloading", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDownloadsTrackers_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.GET("/api/downloads/trackers", DownloadsTrackers(store))

	req := httptest.NewRequest("GET", "/api/downloads/trackers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDownloadsCategories_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.GET("/api/downloads/categories", DownloadsCategories(store))

	req := httptest.NewRequest("GET", "/api/downloads/categories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDownloadsPauseAll_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.PATCH("/api/downloads/pause-all", DownloadsPauseAll(store))

	req := httptest.NewRequest("PATCH", "/api/downloads/pause-all", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp map[string]int
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["affected"] != 0 {
		t.Errorf("affected = %d, want 0", resp["affected"])
	}
}

func TestDownloadsResumeAll_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.PATCH("/api/downloads/resume-all", DownloadsResumeAll(store))

	req := httptest.NewRequest("PATCH", "/api/downloads/resume-all", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDownloadsBatchPause_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.PATCH("/api/downloads/batch/pause", DownloadsBatchPause(store))

	req := httptest.NewRequest("PATCH", "/api/downloads/batch/pause", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsBatchResume_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.PATCH("/api/downloads/batch/resume", DownloadsBatchResume(store))

	req := httptest.NewRequest("PATCH", "/api/downloads/batch/resume", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsBatchDelete_EmptyIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.POST("/api/downloads/batch/delete", DownloadsBatchDelete(store))

	body := map[string][]int{"ids": {}}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/downloads/batch/delete", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDownloadsRecheck_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/downloads/:id/recheck", DownloadsRecheck(store, s))

	req := httptest.NewRequest("POST", "/api/downloads/notanumber/recheck", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsDetails_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/downloads/:id/details", DownloadsDetails(store, s))

	req := httptest.NewRequest("GET", "/api/downloads/notanumber/details", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsUsers_NoAuthStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.GET("/api/downloads/users", DownloadsUsers(store, nil))

	req := httptest.NewRequest("GET", "/api/downloads/users", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDownloadsListAll_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.GET("/api/downloads/all", DownloadsListAll(store, nil, nil))

	req := httptest.NewRequest("GET", "/api/downloads/all", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDownloadParseRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("missing body", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/download", bytes.NewReader([]byte(`{}`)))
		c.Request.Header.Set("Content-Type", "application/json")
		parseDownloadRequest(c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/download", bytes.NewReader([]byte(`not json`)))
		c.Request.Header.Set("Content-Type", "application/json")
		parseDownloadRequest(c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})
}

func TestResolveDownloadClient(t *testing.T) {
	cfg := makeTestConfig(
		config.DownloadClient{ID: "a", Name: "A", Type: "qbittorrent", Default: false},
		config.DownloadClient{ID: "b", Name: "B", Type: "qbittorrent", Default: true},
	)

	t.Run("empty id picks default", func(t *testing.T) {
		dc := resolveDownloadClient(cfg, "")
		if dc == nil || dc.ID != "b" {
			t.Errorf("got %v, want client 'b'", dc)
		}
	})

	t.Run("specific id", func(t *testing.T) {
		dc := resolveDownloadClient(cfg, "a")
		if dc == nil || dc.ID != "a" {
			t.Errorf("got %v, want client 'a'", dc)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		dc := resolveDownloadClient(cfg, "nonexistent")
		if dc != nil {
			t.Errorf("got %v, want nil", dc)
		}
	})

	t.Run("no clients", func(t *testing.T) {
		empty := &config.Config{}
		dc := resolveDownloadClient(empty, "")
		if dc != nil {
			t.Errorf("got %v, want nil", dc)
		}
	})
}
