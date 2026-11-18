package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/handlers"
	"github.com/lgldsilva/jackui/internal/subtitles"
)

func subtitlesRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "4242.vtt"), []byte("WEBVTT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(corsMiddleware())
	r.GET("/api/subtitles/download/:fileId", handlers.SubtitlesDownload(subtitles.New("key", "", "", dir)))
	return r
}

func subtitlesReq(r *gin.Engine, method, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/subtitles/download/4242", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
		if method == http.MethodOptions {
			req.Header.Set("Access-Control-Request-Method", "GET")
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// The 30-day immutable cache must not hand a no-Origin copy (no ACAO) to a
// later cross-origin <track crossorigin> load, hence Vary: Origin.
func TestSubtitlesDownload_CORSViaGlobalMiddlewareWithVary(t *testing.T) {
	r := subtitlesRouter(t)

	w := subtitlesReq(r, http.MethodGet, "https://other.example")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("ACAO = %q, want *", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("ACAC = %q, want empty", got)
	}
	if !strings.Contains(w.Header().Get("Vary"), "Origin") {
		t.Errorf("Vary = %q, want Origin", w.Header().Get("Vary"))
	}

	// Same-origin load: no ACAO, still Vary so caches key on Origin.
	s := subtitlesReq(r, http.MethodGet, "")
	if s.Header().Get("Access-Control-Allow-Origin") != "" || !strings.Contains(s.Header().Get("Vary"), "Origin") {
		t.Errorf("no-Origin headers = %v", s.Header())
	}

	p := subtitlesReq(r, http.MethodOptions, "https://other.example")
	if p.Code != http.StatusNoContent || p.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("preflight = %d %v", p.Code, p.Header())
	}
}
