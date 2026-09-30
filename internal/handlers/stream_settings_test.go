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
		t.Errorf("defaults not filled in: %+v", resp.Defaults)
	}
	if resp.StorageBackend != config.StorageBackendFile {
		t.Errorf("backend = %q, want file", resp.StorageBackend)
	}
}

// GET with a streamer present reflects the LIVE rate limits (source of truth).
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
		t.Errorf("live rate limits = down %d up %d, want %d/%d", resp.MaxDownloadRate, resp.MaxUploadRate, 7<<20, 3<<20)
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
		t.Errorf("negative rate: status = %d, want 400; body %s", w.Code, w.Body.String())
	}
}

func TestStreamUpdateSettings_RejectsBadBackend(t *testing.T) {
	cfg, path := newTestConfig(t)
	w := putSettings(t, cfg, path, nil, `{"storageBackend":"bogus"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid backend: status = %d, want 400", w.Code)
	}
}

func TestStreamUpdateSettings_RejectsNegativeInt(t *testing.T) {
	cfg, path := newTestConfig(t)
	w := putSettings(t, cfg, path, nil, `{"readaheadMB":-5,"storageBackend":"file"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("negative readahead: status = %d, want 400", w.Code)
	}
}

func TestStreamUpdateSettings_RejectsInvalidJSON(t *testing.T) {
	cfg, path := newTestConfig(t)
	w := putSettings(t, cfg, path, nil, `not json`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid json: status = %d, want 400", w.Code)
	}
}

// A PUT that changes only rate limits/readahead does NOT require a restart and persists to the config.
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
		t.Errorf("restartRequired = %v, want false", resp["restartRequired"])
	}
	// Persisted to the config.
	if cfg.Stream.MaxDownloadRate != 1048576 || cfg.Stream.ReadaheadMB != 16 {
		t.Errorf("config did not persist: %+v", cfg.Stream)
	}
	// Applied live to the streamer.
	if down, _ := s.RateLimits(); down != 1048576 {
		t.Errorf("live rate limit = %d, want 1048576", down)
	}
	if s.StreamReadaheadForTesting() != 16<<20 {
		t.Errorf("live readahead = %d, want %d", s.StreamReadaheadForTesting(), 16<<20)
	}
}

// A PUT with hlsMediaRenditions persists to the cfg (read live by StreamHLSMaster)
// and comes back on GET; no restart required.
func TestStreamUpdateSettings_HLSMediaRenditions(t *testing.T) {
	cfg, path := newTestConfig(t)
	s := streamer.NewForTesting()
	w := putSettings(t, cfg, path, s, `{"storageBackend":"file","hlsMediaRenditions":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["restartRequired"] != false {
		t.Errorf("restartRequired = %v, want false", resp["restartRequired"])
	}
	if !cfg.Stream.HLSMediaRenditions {
		t.Error("cfg.Stream.HLSMediaRenditions did not persist true")
	}
	// GET reflects the value.
	gr := gin.New()
	gr.GET("/s", StreamGetSettings(cfg, s))
	gw := httptest.NewRecorder()
	gr.ServeHTTP(gw, httptest.NewRequest("GET", "/s", nil))
	var got map[string]any
	json.Unmarshal(gw.Body.Bytes(), &got)
	if got["hlsMediaRenditions"] != true {
		t.Errorf("GET hlsMediaRenditions = %v, want true", got["hlsMediaRenditions"])
	}
}

// A PUT with seedTrackers persists the cleaned list (no blank lines) and does not
// require a restart (applied live).
func TestStreamUpdateSettings_SeedTrackers(t *testing.T) {
	cfg, path := newTestConfig(t)
	s := streamer.NewForTesting()
	w := putSettings(t, cfg, path, s, `{"storageBackend":"file","seedTrackers":["amigos-share","  ","other"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["restartRequired"] != false {
		t.Errorf("restartRequired = %v, want false", resp["restartRequired"])
	}
	if len(cfg.Stream.SeedTrackers) != 2 || cfg.Stream.SeedTrackers[0] != "amigos-share" || cfg.Stream.SeedTrackers[1] != "other" {
		t.Errorf("seedTrackers not cleaned/persisted: %#v", cfg.Stream.SeedTrackers)
	}
}

func TestCleanSeedTrackers(t *testing.T) {
	got := cleanSeedTrackers([]string{" a ", "", "  ", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("cleanSeedTrackers = %#v", got)
	}
}

// A PUT that changes backend/conns/cache REQUIRES a restart.
func TestStreamUpdateSettings_BootFieldsRequireRestart(t *testing.T) {
	cfg, path := newTestConfig(t)
	w := putSettings(t, cfg, path, nil, `{"storageBackend":"mmap","maxConnsPerTorrent":120,"maxCacheGB":50}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["restartRequired"] != true {
		t.Errorf("restartRequired = %v, want true", resp["restartRequired"])
	}
	if cfg.Stream.StorageBackend != config.StorageBackendMmap || cfg.Stream.MaxConnsPerTorrent != 120 {
		t.Errorf("config did not persist boot fields: %+v", cfg.Stream)
	}
}
