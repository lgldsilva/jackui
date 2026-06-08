package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/local"
)

func TestMountsGet_EmptyArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	router := gin.New()
	router.GET("/api/mounts", MountsGet(cfg))

	req := httptest.NewRequest("GET", "/api/mounts", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "[]" {
		t.Fatalf("expected 200 + [], got %d %q", w.Code, w.Body.String())
	}
}

func TestMountsGet_ReturnsAllowedUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.External.Mounts = []config.ExternalMount{
		{Name: "GDrive", Path: "/mnt/g", AllowedUsers: []string{"admin"}},
	}
	router := gin.New()
	router.GET("/api/mounts", MountsGet(cfg))
	req := httptest.NewRequest("GET", "/api/mounts", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var got []config.ExternalMount
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got) != 1 || len(got[0].AllowedUsers) != 1 || got[0].AllowedUsers[0] != "admin" {
		t.Fatalf("admin endpoint must expose allowedUsers, got %s", w.Body.String())
	}
}

func TestMountsUpdate_ValidatesAndAppliesLive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	configPath := t.TempDir() + "/config.yaml"
	browser := local.NewBrowser(nil)
	router := gin.New()
	router.PUT("/api/mounts", MountsUpdate(cfg, configPath, browser))

	body, _ := json.Marshal([]config.ExternalMount{
		{Name: "Media", Path: "/mnt/media", UserSubpath: true},
		{Name: "Sensitive", Path: "/mnt/s", AllowedUsers: []string{"admin"}},
	})
	req := httptest.NewRequest("PUT", "/api/mounts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	// Config updated + applied live to the browser.
	if len(cfg.External.Mounts) != 2 {
		t.Errorf("config not updated: %+v", cfg.External.Mounts)
	}
	// "admin" sees both; an anonymous user sees only the public per-user one.
	if got := browser.MountsFor("admin"); len(got) != 2 {
		t.Errorf("admin should see 2 mounts live, got %d", len(got))
	}
	if got := browser.MountsFor(""); len(got) != 1 {
		t.Errorf("anonymous should see only the non-restricted mount, got %d", len(got))
	}
}

func TestMountsUpdate_RejectsMissingNameOrPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	configPath := t.TempDir() + "/config.yaml"
	router := gin.New()
	router.PUT("/api/mounts", MountsUpdate(cfg, configPath, local.NewBrowser(nil)))

	for _, bad := range [][]config.ExternalMount{
		{{Name: "", Path: "/x"}},
		{{Name: "X", Path: ""}},
		{{Name: "Dup", Path: "/a"}, {Name: "Dup", Path: "/b"}},
	} {
		body, _ := json.Marshal(bad)
		req := httptest.NewRequest("PUT", "/api/mounts", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for %+v, got %d", bad, w.Code)
		}
	}
}
