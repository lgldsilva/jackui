package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestTmdbMatch_MissingTitle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, TmdbMatch(nil), http.MethodGet, "/api/tmdb/match", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestTmdbMatch_Disabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, TmdbMatch(nil), http.MethodGet, "/api/tmdb/match?title=Inception", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestTmdbMatchBatch_MissingTitles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, TmdbMatchBatch(nil), http.MethodPost, "/api/tmdb/match/batch", `{"titles":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestTmdbMatchBatch_Disabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, TmdbMatchBatch(nil), http.MethodPost, "/api/tmdb/match/batch", `{"titles":["Inception"]}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestTmdbMatchBatch_TooMany_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	titles := make([]string, 101)
	for i := range titles {
		titles[i] = "title"
	}
	body := `{"titles":[` + strings.Repeat(`"title",`, 100) + `"title"]}`
	w := invokeCoverageHandler(t, TmdbMatchBatch(nil), http.MethodPost, "/api/tmdb/match/batch", body)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestTmdbTrending_Disabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, TmdbTrending(nil), http.MethodGet, "/api/tmdb/trending", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestTmdbVideos_InvalidParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, TmdbVideos(nil), http.MethodGet, "/api/tmdb/videos?kind=bad&id=123", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestTmdbVideos_Disabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, TmdbVideos(nil), http.MethodGet, "/api/tmdb/videos?kind=movie&id=123", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestTmdbGenres_Disabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, TmdbGenres(nil), http.MethodGet, "/api/tmdb/genres", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}
