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
	"github.com/luizg/jackui/internal/config"
)

func TestGetConfig_ReturnsExpectedStructure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Port: 8989,
	}
	cfg.Jackett.URL = "http://localhost:9117"
	cfg.Jackett.APIKey = "secret"
	cfg.DownloadClients = []config.DownloadClient{
		{ID: "q1", Name: "qBit", Type: "qbittorrent", URL: "http://qb:8080", Username: "admin", Password: "pass", Default: true},
	}

	router := gin.New()
	router.GET("/api/config", GetConfig(cfg, ""))

	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp configResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal("decode error:", err)
	}
	if resp.Port != 8989 {
		t.Errorf("port = %d, want 8989", resp.Port)
	}
	if resp.Jackett.URL != "http://localhost:9117" {
		t.Errorf("jackett URL = %q", resp.Jackett.URL)
	}
	if resp.Jackett.APIKey != "" {
		t.Error("API key must be omitted from GET response")
	}
	if !resp.Jackett.APIKeySet {
		t.Error("APIKeySet should be true when key is present")
	}
	if len(resp.Clients) != 1 {
		t.Fatalf("len(clients) = %d, want 1", len(resp.Clients))
	}
	if resp.Clients[0].Password != "" {
		t.Error("password must be omitted from GET response")
	}
}

func TestGetConfig_ApiKeyNotSet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Port: 8989}
	cfg.Jackett.URL = "http://localhost:9117"

	router := gin.New()
	router.GET("/api/config", GetConfig(cfg, ""))

	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var resp configResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Jackett.APIKeySet {
		t.Error("APIKeySet should be false when no key")
	}
}

func TestUpdateConfig_SavesToFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := &config.Config{Port: 8989}
	cfg.Jackett.URL = "http://localhost:9117"

	router := gin.New()
	router.PUT("/api/config", UpdateConfig(cfg, configPath, nil, nil))

	body := configUpdateRequest{
		Port: 9000,
		Jackett: jackettConfigResponse{
			URL: "http://new-jackett:9117",
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if cfg.Port != 9000 {
		t.Errorf("cfg.Port = %d, want 9000", cfg.Port)
	}
	if cfg.Jackett.URL != "http://new-jackett:9117" {
		t.Errorf("Jackett URL = %q", cfg.Jackett.URL)
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("config file was not saved")
	}
}

func TestUpdateConfig_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	router := gin.New()
	router.PUT("/api/config", UpdateConfig(cfg, "", nil, nil))

	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestUpdateConfig_KeepsApiKeyWhenOmitted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Port: 8989}
	cfg.Jackett.URL = "http://localhost:9117"
	cfg.Jackett.APIKey = "existing-key"

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	router := gin.New()
	router.PUT("/api/config", UpdateConfig(cfg, configPath, nil, nil))

	body := configUpdateRequest{
		Port: 9000,
		Jackett: jackettConfigResponse{
			URL:    "http://new:9117",
			APIKey: "",
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if cfg.Jackett.APIKey != "existing-key" {
		t.Errorf("API key changed to %q, want 'existing-key'", cfg.Jackett.APIKey)
	}
}

func TestUpdateConfig_OverwritesApiKeyWhenProvided(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Port: 8989}
	cfg.Jackett.APIKey = "old-key"

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	router := gin.New()
	router.PUT("/api/config", UpdateConfig(cfg, configPath, nil, nil))

	body := configUpdateRequest{
		Jackett: jackettConfigResponse{
			URL:    "http://j:9117",
			APIKey: "new-key",
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if cfg.Jackett.APIKey != "new-key" {
		t.Errorf("API key = %q, want 'new-key'", cfg.Jackett.APIKey)
	}
}

func TestUpdateConfig_ClientPasswordPreserved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := &config.Config{Port: 8989}
	cfg.DownloadClients = []config.DownloadClient{
		{ID: "q1", Name: "qBit", Type: "qbittorrent", Password: "old-pass", Default: true},
	}

	router := gin.New()
	router.PUT("/api/config", UpdateConfig(cfg, configPath, nil, nil))

	body := configUpdateRequest{
		Clients: []downloadClientResponse{
			{ID: "q1", Name: "qBit", Type: "qbittorrent", Password: "", Default: true},
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if cfg.DownloadClients[0].Password != "old-pass" {
		t.Errorf("password = %q, want 'old-pass'", cfg.DownloadClients[0].Password)
	}
}

func TestUpdateConfig_ClientPasswordOverwritten(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := &config.Config{Port: 8989}
	cfg.DownloadClients = []config.DownloadClient{
		{ID: "q1", Name: "qBit", Type: "qbittorrent", Password: "old-pass", Default: true},
	}

	router := gin.New()
	router.PUT("/api/config", UpdateConfig(cfg, configPath, nil, nil))

	body := configUpdateRequest{
		Clients: []downloadClientResponse{
			{ID: "q1", Name: "qBit", Type: "qbittorrent", Password: "new-pass", Default: true},
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if cfg.DownloadClients[0].Password != "new-pass" {
		t.Errorf("password = %q, want 'new-pass'", cfg.DownloadClients[0].Password)
	}
}

func TestUpdateConfig_NewClientAdded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := &config.Config{Port: 8989}

	router := gin.New()
	router.PUT("/api/config", UpdateConfig(cfg, configPath, nil, nil))

	body := configUpdateRequest{
		Clients: []downloadClientResponse{
			{ID: "q1", Name: "New qBit", Type: "qbittorrent", Password: "new-pass", Default: true},
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if len(cfg.DownloadClients) != 1 {
		t.Fatalf("len(clients) = %d, want 1", len(cfg.DownloadClients))
	}
	if cfg.DownloadClients[0].Password != "new-pass" {
		t.Errorf("password = %q, want 'new-pass'", cfg.DownloadClients[0].Password)
	}
}

func TestTestJackett_WithRealServer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.Jackett.URL = srv.URL
	cfg.Jackett.APIKey = "test"

	router := gin.New()
	router.POST("/api/config/test", TestJackett(cfg))

	req := httptest.NewRequest("POST", "/api/config/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != true {
		t.Error("expected success=true")
	}
}

func TestTestJackett_ServerError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.Jackett.URL = srv.URL

	router := gin.New()
	router.POST("/api/config/test", TestJackett(cfg))

	req := httptest.NewRequest("POST", "/api/config/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}
