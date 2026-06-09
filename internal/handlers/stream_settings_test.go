package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func newTestConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := &config.Config{}
	cfg.Stream.StorageBackend = config.StorageBackendFile
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	return cfg, path
}

func TestStreamGetSettings_ReturnsDefaults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg, _ := newTestConfig(t)
	r := gin.New()
	r.GET("/s", StreamGetSettings(cfg, nil))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/s", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp streamSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Defaults.MaxConnsPerTorrent != defMaxConnsPerTorrent || resp.Defaults.ReadaheadMB != defReadaheadMB {
		t.Errorf("defaults não preenchidos: %+v", resp.Defaults)
	}
	if resp.StorageBackend != config.StorageBackendFile {
		t.Errorf("backend = %q, queria file", resp.StorageBackend)
	}
}

// GET com streamer presente reflete os rate limits AO VIVO (fonte da verdade).
func TestStreamGetSettings_LiveRateLimitsFromStreamer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg, _ := newTestConfig(t)
	s := streamer.NewForTesting()
	s.SetRateLimits(7<<20, 3<<20)

	r := gin.New()
	r.GET("/s", StreamGetSettings(cfg, s))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/s", nil))

	var resp streamSettingsResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.MaxDownloadRate != 7<<20 || resp.MaxUploadRate != 3<<20 {
		t.Errorf("rate limits ao vivo = down %d up %d, queria %d/%d", resp.MaxDownloadRate, resp.MaxUploadRate, 7<<20, 3<<20)
	}
}

func putSettings(t *testing.T, cfg *config.Config, path string, s *streamer.Streamer, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/s", StreamUpdateSettings(cfg, path, s))
	req := httptest.NewRequest("PUT", "/s", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestStreamUpdateSettings_RejectsNegative(t *testing.T) {
	cfg, path := newTestConfig(t)
	w := putSettings(t, cfg, path, nil, `{"maxDownloadRate":-1,"storageBackend":"file"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("rate negativo: status = %d, want 400; body %s", w.Code, w.Body.String())
	}
}

func TestStreamUpdateSettings_RejectsBadBackend(t *testing.T) {
	cfg, path := newTestConfig(t)
	w := putSettings(t, cfg, path, nil, `{"storageBackend":"bogus"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("backend inválido: status = %d, want 400", w.Code)
	}
}

func TestStreamUpdateSettings_RejectsNegativeInt(t *testing.T) {
	cfg, path := newTestConfig(t)
	w := putSettings(t, cfg, path, nil, `{"readaheadMB":-5,"storageBackend":"file"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("readahead negativo: status = %d, want 400", w.Code)
	}
}

func TestStreamUpdateSettings_RejectsInvalidJSON(t *testing.T) {
	cfg, path := newTestConfig(t)
	w := putSettings(t, cfg, path, nil, `not json`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("json inválido: status = %d, want 400", w.Code)
	}
}

// PUT que muda só rate limits/readahead NÃO exige reinício e persiste na config.
func TestStreamUpdateSettings_LiveFieldsNoRestart(t *testing.T) {
	cfg, path := newTestConfig(t)
	s := streamer.NewForTesting()
	w := putSettings(t, cfg, path, s, `{"maxDownloadRate":1048576,"readaheadMB":16,"storageBackend":"file"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["restartRequired"] != false {
		t.Errorf("restartRequired = %v, queria false", resp["restartRequired"])
	}
	// Persistiu na config.
	if cfg.Stream.MaxDownloadRate != 1048576 || cfg.Stream.ReadaheadMB != 16 {
		t.Errorf("config não persistiu: %+v", cfg.Stream)
	}
	// Aplicou ao vivo no streamer.
	if down, _ := s.RateLimits(); down != 1048576 {
		t.Errorf("rate limit ao vivo = %d, queria 1048576", down)
	}
	if s.StreamReadaheadForTesting() != 16<<20 {
		t.Errorf("readahead ao vivo = %d, queria %d", s.StreamReadaheadForTesting(), 16<<20)
	}
}

// PUT que muda backend/conns/cache EXIGE reinício.
func TestStreamUpdateSettings_BootFieldsRequireRestart(t *testing.T) {
	cfg, path := newTestConfig(t)
	w := putSettings(t, cfg, path, nil, `{"storageBackend":"mmap","maxConnsPerTorrent":120,"maxCacheGB":50}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["restartRequired"] != true {
		t.Errorf("restartRequired = %v, queria true", resp["restartRequired"])
	}
	if cfg.Stream.StorageBackend != config.StorageBackendMmap || cfg.Stream.MaxConnsPerTorrent != 120 {
		t.Errorf("config não persistiu campos de boot: %+v", cfg.Stream)
	}
}
