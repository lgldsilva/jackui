package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestDropHiddenHelpers(t *testing.T) {
	hidden := map[string]bool{"h1": true}

	dl := []downloads.Download{{InfoHash: "h1"}, {InfoHash: "h2"}}
	if got := dropHiddenDownloads(dl, hiddenDownloadFilter{hashes: hidden}); len(got) != 1 || got[0].InfoHash != "h2" {
		t.Errorf("dropHiddenDownloads = %+v", got)
	}
	if got := dropHiddenDownloads(dl, hiddenDownloadFilter{}); len(got) != 2 {
		t.Errorf("empty filter should be no-op, got %d", len(got))
	}

	lib := []library.Entry{{InfoHash: "h1"}, {InfoHash: "h2"}}
	if got := dropHiddenLibrary(lib, hidden); len(got) != 1 || got[0].InfoHash != "h2" {
		t.Errorf("dropHiddenLibrary = %+v", got)
	}

	ents := []local.Entry{{Path: "secret"}, {Path: "ok"}}
	if got := dropHiddenLocalEntries(ents, map[string]bool{"secret": true}); len(got) != 1 || got[0].Path != "ok" {
		t.Errorf("dropHiddenLocalEntries = %+v", got)
	}
}

// newCurtainStreamer builds a test streamer with a real favourites store.
func newCurtainStreamer(t *testing.T) (*streamer.Streamer, *streamer.FavoritesStore) {
	t.Helper()
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(filepath.Join(t.TempDir(), "fav.db"))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(func() { fav.Close() })
	s.SetFavorites(fav)
	return s, fav
}

// LocalSetHidden persists a path; LocalListHidden reads it back.
func TestLocalHiddenEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "secret"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := local.NewBrowser([]config.ExternalMount{{Name: "M", Path: dir}})
	s, _ := newCurtainStreamer(t)

	router := gin.New()
	router.Use(middleware.RevealHidden())
	router.POST("/api/local/hidden", LocalSetHidden(b, s))
	router.GET("/api/local/hidden", LocalListHidden(s))
	router.GET("/api/local/list", LocalList(b, s))

	// Hide "secret".
	body := `{"mount":"M","path":"secret","hidden":true}`
	req := httptest.NewRequest("POST", "/api/local/hidden", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST hidden: status %d, body %s", w.Code, w.Body.String())
	}

	// LocalListHidden returns it.
	req = httptest.NewRequest("GET", "/api/local/hidden", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var paths []streamer.HiddenLocalPath
	if err := json.Unmarshal(w.Body.Bytes(), &paths); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0].Path != "secret" {
		t.Fatalf("LocalListHidden = %+v", paths)
	}

	// Default LocalList hides "secret".
	req = httptest.NewRequest("GET", "/api/local/list?mount=M", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), "secret") {
		t.Errorf("hidden dir leaked into default list: %s", w.Body.String())
	}

	// With the curtain open, "secret" shows.
	req = httptest.NewRequest("GET", "/api/local/list?mount=M", nil)
	req.Header.Set("X-JackUI-Reveal-Hidden", "1")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "secret") {
		t.Errorf("revealed list should contain secret: %s", w.Body.String())
	}
}

// LocalSetHidden rejects a body missing mount/path.
func TestLocalSetHidden_BadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	s, _ := newCurtainStreamer(t)
	router := gin.New()
	router.POST("/api/local/hidden", LocalSetHidden(b, s))

	req := httptest.NewRequest("POST", "/api/local/hidden", strings.NewReader(`{"mount":"M"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing path: status %d, want 400", w.Code)
	}
}
