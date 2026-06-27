package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/watchlist"
)

func statsRouter(lib *library.Store, dl *downloads.Store, hist *history.Store, wl *watchlist.Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/stats", func(c *gin.Context) { setAuth(c, 1, false) }, Stats(lib, dl, hist, wl))
	return r
}

func TestStats_AllStoresNilReturnsZeroes(t *testing.T) {
	r := statsRouter(nil, nil, nil, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/stats", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	libAgg, _ := out["library"].(map[string]any)
	if libAgg == nil || libAgg["titles"].(float64) != 0 {
		t.Fatalf("expected zeroed library agg, got %v", out["library"])
	}
}

func TestStats_LibraryError500(t *testing.T) {
	// Close the underlying pool (Store.Close is a no-op now) so List fails → 500.
	pool := seededPool(t)
	lib, err := library.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	r := statsRouter(lib, nil, nil, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/stats", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestStats_DownloadsError500(t *testing.T) {
	dl := hgEDownloads(t)
	dl.Close()
	r := statsRouter(nil, dl, nil, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/stats", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestStats_AggregatesSeededStores(t *testing.T) {
	lib := hgELibrary(t)
	dl := hgEDownloads(t)
	hist := hgEHistory(t)
	wl := hgEWatchlist(t)

	e, err := lib.Upsert(library.UpsertInput{UserID: 1, InfoHash: hgEHash, Magnet: "m", Name: "Movie", Kind: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.UpdateResume(e.ID, 1, 95, 100, -1, false); err != nil {
		t.Fatal(err)
	}
	if _, err := dl.Create(downloads.Download{UserID: 1, InfoHash: hgEHash, Magnet: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := hist.Save("matrix", []jackett.Result{{Title: "X", InfoHash: hgEHash}}, 1, false); err != nil {
		t.Fatal(err)
	}
	// The watchlist store stays empty on purpose: seeding it would couple this
	// test to the Create signature, which another in-flight PR is changing.

	r := statsRouter(lib, dl, hist, wl)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/stats", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	var out struct {
		Library struct {
			Titles    int `json:"titles"`
			Completed int `json:"completed"`
		} `json:"library"`
		Downloads struct {
			Total int `json:"total"`
		} `json:"downloads"`
		SearchQueries int `json:"searchQueries"`
		Watchlists    struct {
			Count int `json:"count"`
			Hits  int `json:"hits"`
		} `json:"watchlists"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Library.Titles != 1 || out.Library.Completed != 1 {
		t.Fatalf("library agg = %+v", out.Library)
	}
	if out.Downloads.Total != 1 {
		t.Fatalf("downloads total = %d", out.Downloads.Total)
	}
	if out.SearchQueries != 1 {
		t.Fatalf("searchQueries = %d", out.SearchQueries)
	}
	if out.Watchlists.Count != 0 || out.Watchlists.Hits != 0 {
		t.Fatalf("watchlists = %+v, want zeroes (empty store)", out.Watchlists)
	}
}
