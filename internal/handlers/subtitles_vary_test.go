package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/subtitles"
)

// Success path (served from the VTT disk cache): immutable caching must be
// keyed on Origin because CORS headers are added only to cross-origin requests.
func TestSubtitlesDownload_SuccessHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "4242.vtt"), []byte("WEBVTT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.GET("/s/:fileId", SubtitlesDownload(subtitles.New("key", "", "", dir)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/s/4242", nil))

	if w.Code != http.StatusOK || w.Body.String() != "WEBVTT\n" {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Vary"), "Origin") {
		t.Errorf("Vary = %q", w.Header().Get("Vary"))
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("handler must leave CORS to the global middleware")
	}
	if w.Header().Get("ETag") != `"sub-4242"` || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("headers = %v", w.Header())
	}
}
