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
)

func TestDownloadsSetPriority_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	router := gin.New()
	router.PATCH("/api/downloads/:id/priority", DownloadsSetPriority(store))

	req := httptest.NewRequest("PATCH", "/api/downloads/abc/priority", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsSetPriority_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	router := gin.New()
	router.PATCH("/api/downloads/:id/priority", DownloadsSetPriority(store))

	body, _ := json.Marshal(map[string]string{"priority": "high"})
	req := httptest.NewRequest("PATCH", "/api/downloads/999/priority", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestDownloadsSetPriority_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	d, _ := store.Create(downloads.Download{UserID: 0, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A"})

	router := gin.New()
	router.PATCH("/api/downloads/:id/priority", DownloadsSetPriority(store))

	body, _ := json.Marshal(map[string]string{"priority": "high"})
	req := httptest.NewRequest("PATCH", "/api/downloads/1/priority", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	got, _ := store.Get(0, d.ID)
	if got.Priority != downloads.PriorityHigh {
		t.Errorf("priority not persisted, got %q", got.Priority)
	}
}

func TestDownloadsSetPriority_InvalidValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	_, _ = store.Create(downloads.Download{UserID: 0, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A"})

	router := gin.New()
	router.PATCH("/api/downloads/:id/priority", DownloadsSetPriority(store))

	body, _ := json.Marshal(map[string]string{"priority": "bogus"})
	req := httptest.NewRequest("PATCH", "/api/downloads/1/priority", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid priority", w.Code)
	}
}

func TestDownloadsSources_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	router := gin.New()
	router.GET("/api/downloads/:id/sources", DownloadsSources(store))

	req := httptest.NewRequest("GET", "/api/downloads/999/sources", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestDownloadsSources_EmptyArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	_, _ = store.Create(downloads.Download{UserID: 0, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A"})
	router := gin.New()
	router.GET("/api/downloads/:id/sources", DownloadsSources(store))

	req := httptest.NewRequest("GET", "/api/downloads/1/sources", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	// Must serialize as [] (not null) so the frontend can map over it.
	if body := w.Body.String(); body != "[]" {
		t.Errorf("expected empty array body, got %q", body)
	}
}

func TestDownloadsGetSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.DownloadsQueue = config.DownloadsQueueConfig{MaxActive: 3, StallThresholdMin: 30, MaxStalls: 3, AgingStepMin: 60, AgingCap: 150}

	router := gin.New()
	router.GET("/api/downloads/settings", DownloadsGetSettings(cfg))
	req := httptest.NewRequest("GET", "/api/downloads/settings", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got downloadsQueueBody
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.MaxActive != 3 || got.StallThresholdMin != 30 {
		t.Errorf("unexpected settings body: %+v", got)
	}
}

func TestDownloadsUpdateSettings_Validation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	configPath := t.TempDir() + "/config.yaml"

	router := gin.New()
	router.PUT("/api/downloads/settings", DownloadsUpdateSettings(cfg, configPath))

	// maxActive < 1 → 400
	body, _ := json.Marshal(downloadsQueueBody{MaxActive: 0, StallThresholdMin: 30, MaxStalls: 3})
	req := httptest.NewRequest("PUT", "/api/downloads/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for maxActive<1", w.Code)
	}
}

func TestDownloadsUpdateSettings_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	configPath := t.TempDir() + "/config.yaml"

	router := gin.New()
	router.PUT("/api/downloads/settings", DownloadsUpdateSettings(cfg, configPath))

	body, _ := json.Marshal(downloadsQueueBody{
		MaxActive: 5, StallThresholdMin: 15, MaxStalls: 2, AgingStepMin: 30, AgingCap: 100, RotationEnabled: true,
	})
	req := httptest.NewRequest("PUT", "/api/downloads/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if cfg.DownloadsQueue.MaxActive != 5 || !cfg.DownloadsQueue.RotationEnabled {
		t.Errorf("config not updated: %+v", cfg.DownloadsQueue)
	}
}
