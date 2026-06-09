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
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// UpdateConfig must also refresh the live Jackett client and the streamer's
// SSRF-guard host, so a saved config takes effect without a restart.
func TestUpdateConfig_RefreshesLiveClients(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{Port: 8989}
	jc := jackett.New("http://old.host:9117", "old-key")
	srv := streamer.NewForTesting()

	router := gin.New()
	router.PUT("/api/config", UpdateConfig(cfg, configPath, jc, srv))

	body, _ := json.Marshal(configUpdateRequest{
		Port:    9000,
		Jackett: jackettConfigResponse{URL: "http://new.host:9117", APIKey: "new-key"},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if jc.URL != "http://new.host:9117" {
		t.Errorf("live jackett URL = %q, want new.host", jc.URL)
	}
	if jc.APIKey != "new-key" {
		t.Errorf("live jackett APIKey = %q, want new-key", jc.APIKey)
	}
}
