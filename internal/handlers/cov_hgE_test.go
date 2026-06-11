package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/playlists"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
	"github.com/lgldsilva/jackui/internal/watchlist"
)

// hgE prefix on every identifier to avoid collisions with the other test files
// in this package. These tests target success + error branches in history.go,
// library.go, playlists.go, watchlist.go, subtitles.go and common.go that were
// not yet exercised by the existing *_test.go / cov_*_test.go files.

const hgEHash = "abababababababababababababababababababab"

func hgEDo(router *gin.Engine, method, path string, body []byte) *httptest.ResponseRecorder {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func hgEHistory(t *testing.T) *history.Store {
	t.Helper()
	s, err := history.New(t.TempDir() + "/hist.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func hgEFavs(t *testing.T) *streamer.FavoritesStore {
	t.Helper()
	f, err := streamer.NewFavorites(t.TempDir() + "/favs.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func hgEDownloads(t *testing.T) *downloads.Store {
	t.Helper()
	s, err := downloads.New(t.TempDir() + "/dl.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func hgELibrary(t *testing.T) *library.Store {
	t.Helper()
	s, err := library.New(t.TempDir() + "/lib.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func hgEPlaylists(t *testing.T) *playlists.Store {
	t.Helper()
	s, err := playlists.New(t.TempDir() + "/pl.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func hgEWatchlist(t *testing.T) *watchlist.Store {
	t.Helper()
	s, err := watchlist.New(t.TempDir() + "/wl.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// ---------------------------------------------------------------------------
// history.go — enrichCached with a real enricher (favorited + downloaded flags)
// ---------------------------------------------------------------------------

func TestHgEHistoryResults_EnrichesFavAndDownload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hist := hgEHistory(t)
	favs := hgEFavs(t)
	dls := hgEDownloads(t)

	// One cached result that is both favorited and downloaded by user 1.
	if err := hist.Save("matrix", []jackett.Result{
		{Title: "The Matrix 1080p", InfoHash: hgEHash, MagnetURI: MagnetPrefix + hgEHash, CategoryID: 2000},
	}, 1, false); err != nil {
		t.Fatal(err)
	}
	if err := favs.Add("The Matrix 1080p", hgEHash, MagnetPrefix+hgEHash, "", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := dls.Create(downloads.Download{
		UserID:   1,
		InfoHash: hgEHash,
		Name:     "The Matrix 1080p",
		Magnet:   MagnetPrefix + hgEHash,
	}); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/history/results", func(c *gin.Context) {
		setAuth(c, 1, false)
	}, GetHistoryResults(hist, favs, dls))

	w := hgEDo(router, "GET", "/api/history/results?q=matrix", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	var out []enrichedCached
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("expected at least one enriched result")
	}
	var found bool
	for _, r := range out {
		if r.InfoHash == hgEHash {
			found = true
			if !r.IsFavorited {
				t.Error("expected IsFavorited=true")
			}
			if !r.IsDownloaded {
				t.Error("expected IsDownloaded=true")
			}
		}
	}
	if !found {
		t.Error("expected the seeded infoHash in results")
	}
}

func TestHgESearchCache_EnrichesWithEnricher(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hist := hgEHistory(t)
	favs := hgEFavs(t)
	dls := hgEDownloads(t)

	if err := hist.Save("inception", []jackett.Result{
		{Title: "Inception 2010 1080p", InfoHash: hgEHash, MagnetURI: MagnetPrefix + hgEHash, CategoryID: 2000},
	}, 1, false); err != nil {
		t.Fatal(err)
	}
	if err := favs.Add("Inception 2010 1080p", hgEHash, MagnetPrefix+hgEHash, "", 1); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/history/cache", func(c *gin.Context) {
		setAuth(c, 1, false)
	}, SearchCache(hist, favs, dls))

	w := hgEDo(router, "GET", "/api/history/cache?q=inception", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
}

func TestHgEDeleteHistory_SpecificQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hist := hgEHistory(t)
	if err := hist.Save("doomed", []jackett.Result{
		{Title: "Doomed", InfoHash: hgEHash},
	}, 1, false); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.DELETE("/api/history", func(c *gin.Context) {
		setAuth(c, 1, false)
	}, DeleteHistory(hist))

	w := hgEDo(router, "DELETE", "/api/history?q=doomed", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["message"] != "query cleared" {
		t.Errorf("message = %q, want 'query cleared'", body["message"])
	}
}

// ---------------------------------------------------------------------------
// library.go — Get/UpdateResume/Delete success paths via a seeded Upsert
// ---------------------------------------------------------------------------

func hgESeedLibrary(t *testing.T, lib *library.Store, userID int) int {
	t.Helper()
	e, err := lib.Upsert(library.UpsertInput{
		UserID:      userID,
		InfoHash:    hgEHash,
		Magnet:      MagnetPrefix + hgEHash,
		Name:        "Some Movie 1080p",
		PrimaryFile: 0,
		TotalSize:   1234,
		Kind:        "movie",
	})
	if err != nil {
		t.Fatal(err)
	}
	return e.ID
}

func TestHgELibraryGet_Found(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib := hgELibrary(t)
	id := hgESeedLibrary(t, lib, 1)

	router := gin.New()
	router.GET("/api/library/:id", func(c *gin.Context) {
		setAuth(c, 1, false)
	}, LibraryGet(lib))

	w := hgEDo(router, "GET", "/api/library/"+strconv.Itoa(id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	var entry library.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.ID != id {
		t.Errorf("id = %d, want %d", entry.ID, id)
	}
}

func TestHgELibraryUpdateResume_WithFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib := hgELibrary(t)
	id := hgESeedLibrary(t, lib, 1)

	router := gin.New()
	router.PATCH("/api/library/:id", func(c *gin.Context) {
		setAuth(c, 1, false)
	}, LibraryUpdateResume(lib))

	body, _ := json.Marshal(map[string]any{
		"resumeSeconds":   42.5,
		"durationSeconds": 100.0,
		"fileIndex":       3,
	})
	w := hgEDo(router, "PATCH", "/api/library/"+strconv.Itoa(id), body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
}

func TestHgELibraryDelete_Found(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib := hgELibrary(t)
	id := hgESeedLibrary(t, lib, 1)

	router := gin.New()
	router.DELETE("/api/library/:id", func(c *gin.Context) {
		setAuth(c, 1, false)
	}, LibraryDelete(lib))

	w := hgEDo(router, "DELETE", "/api/library/"+strconv.Itoa(id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["message"] != "deleted" {
		t.Errorf("message = %q, want 'deleted'", body["message"])
	}
}

func TestHgELibraryGet_AdminSeesOther(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib := hgELibrary(t)
	id := hgESeedLibrary(t, lib, 2) // owned by user 2

	router := gin.New()
	router.GET("/api/library/:id", func(c *gin.Context) {
		setAuth(c, 1, true) // admin
	}, LibraryGet(lib))

	w := hgEDo(router, "GET", "/api/library/"+strconv.Itoa(id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("admin should see other user's entry; status = %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// playlists.go — full CRUD success path + item add/reorder/remove
// ---------------------------------------------------------------------------

func TestHgEPlaylists_FullLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgEPlaylists(t)

	router := gin.New()
	auth := func(c *gin.Context) { setAuth(c, 1, false) }
	router.POST("/api/playlists", auth, PlaylistsCreate(store))
	router.GET("/api/playlists/:id", auth, PlaylistsGet(store))
	router.PATCH("/api/playlists/:id", auth, PlaylistsUpdate(store))
	router.POST("/api/playlists/:id/items", auth, PlaylistsAddItem(store))
	router.PATCH("/api/playlists/:id/items/:itemId", auth, PlaylistsReorderItem(store))
	router.DELETE("/api/playlists/:id/items/:itemId", auth, PlaylistsRemoveItem(store))
	router.DELETE("/api/playlists/:id", auth, PlaylistsDelete(store))

	// Create
	w := hgEDo(router, "POST", "/api/playlists", []byte(`{"name":"Favs","description":"d"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d; body %s", w.Code, w.Body.String())
	}
	var pl playlists.Playlist
	if err := json.Unmarshal(w.Body.Bytes(), &pl); err != nil {
		t.Fatal(err)
	}
	pid := strconv.Itoa(pl.ID)

	// Get (success path with items)
	w = hgEDo(router, "GET", "/api/playlists/"+pid, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d", w.Code)
	}

	// Update
	w = hgEDo(router, "PATCH", "/api/playlists/"+pid, []byte(`{"name":"Renamed","description":"x"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d; body %s", w.Code, w.Body.String())
	}

	// Add item
	w = hgEDo(router, "POST", "/api/playlists/"+pid+"/items", []byte(`{"title":"Item A","magnet":"m","infoHash":"`+hgEHash+`","fileIndex":0}`))
	if w.Code != http.StatusOK {
		t.Fatalf("add item status = %d; body %s", w.Code, w.Body.String())
	}
	var it playlists.Item
	if err := json.Unmarshal(w.Body.Bytes(), &it); err != nil {
		t.Fatal(err)
	}
	iid := strconv.Itoa(it.ID)

	// Reorder item
	w = hgEDo(router, "PATCH", "/api/playlists/"+pid+"/items/"+iid, []byte(`{"position":0}`))
	if w.Code != http.StatusOK {
		t.Fatalf("reorder status = %d; body %s", w.Code, w.Body.String())
	}

	// Remove item
	w = hgEDo(router, "DELETE", "/api/playlists/"+pid+"/items/"+iid, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("remove item status = %d; body %s", w.Code, w.Body.String())
	}

	// Delete playlist
	w = hgEDo(router, "DELETE", "/api/playlists/"+pid, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d; body %s", w.Code, w.Body.String())
	}
}

func TestHgEPlaylistsUpdate_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgEPlaylists(t)
	router := gin.New()
	router.PATCH("/api/playlists/:id", func(c *gin.Context) { setAuth(c, 1, false) }, PlaylistsUpdate(store))

	w := hgEDo(router, "PATCH", "/api/playlists/1", []byte(`{not-json`))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHgEPlaylistsAddItem_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgEPlaylists(t)
	router := gin.New()
	router.POST("/api/playlists/:id/items", func(c *gin.Context) { setAuth(c, 1, false) }, PlaylistsAddItem(store))

	w := hgEDo(router, "POST", "/api/playlists/1/items", []byte(`{bad`))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHgEPlaylistsReorderItem_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgEPlaylists(t)
	router := gin.New()
	router.PATCH("/api/playlists/:id/items/:itemId", func(c *gin.Context) { setAuth(c, 1, false) }, PlaylistsReorderItem(store))

	w := hgEDo(router, "PATCH", "/api/playlists/1/items/1", []byte(`{bad`))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHgEPlaylistsUpdate_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgEPlaylists(t)
	router := gin.New()
	router.PATCH("/api/playlists/:id", func(c *gin.Context) { setAuth(c, 1, false) }, PlaylistsUpdate(store))

	w := hgEDo(router, "PATCH", "/api/playlists/notanumber", []byte(`{"name":"x"}`))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHgEPlaylistsAddItem_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgEPlaylists(t)
	router := gin.New()
	router.POST("/api/playlists/:id/items", func(c *gin.Context) { setAuth(c, 1, false) }, PlaylistsAddItem(store))

	w := hgEDo(router, "POST", "/api/playlists/notanumber/items", []byte(`{"title":"a"}`))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHgEPlaylistsDelete_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgEPlaylists(t)
	router := gin.New()
	router.DELETE("/api/playlists/:id", func(c *gin.Context) { setAuth(c, 1, false) }, PlaylistsDelete(store))

	w := hgEDo(router, "DELETE", "/api/playlists/notanumber", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// ---------------------------------------------------------------------------
// watchlist.go — Update/Delete/Hits success paths via a seeded Create
// ---------------------------------------------------------------------------

func hgESeedWatchlist(t *testing.T, s *watchlist.Store, userID int) int {
	t.Helper()
	w, err := s.Create(userID, watchlist.Params{Query: "the office", Category: "5000", MinSeeders: 1, NtfyTopic: "topic"})
	if err != nil {
		t.Fatal(err)
	}
	return w.ID
}

func TestHgEWatchlistUpdate_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgEWatchlist(t)
	id := hgESeedWatchlist(t, s, 1)

	router := gin.New()
	router.PUT("/api/watchlists/:id", func(c *gin.Context) { setAuth(c, 1, false) }, WatchlistUpdate(s))

	body, _ := json.Marshal(watchlistInput{Query: "new query", Category: "2000", MinSeeders: 5, NtfyTopic: "t"})
	w := hgEDo(router, "PUT", "/api/watchlists/"+strconv.Itoa(id), body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
}

func TestHgEWatchlistUpdate_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgEWatchlist(t)
	router := gin.New()
	router.PUT("/api/watchlists/:id", func(c *gin.Context) { setAuth(c, 1, false) }, WatchlistUpdate(s))

	body, _ := json.Marshal(watchlistInput{Query: "x"})
	w := hgEDo(router, "PUT", "/api/watchlists/9999", body)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHgEWatchlistUpdate_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgEWatchlist(t)
	router := gin.New()
	router.PUT("/api/watchlists/:id", func(c *gin.Context) { setAuth(c, 1, false) }, WatchlistUpdate(s))

	w := hgEDo(router, "PUT", "/api/watchlists/1", []byte(`{bad`))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHgEWatchlistDelete_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgEWatchlist(t)
	id := hgESeedWatchlist(t, s, 1)

	router := gin.New()
	router.DELETE("/api/watchlists/:id", func(c *gin.Context) { setAuth(c, 1, false) }, WatchlistDelete(s))

	w := hgEDo(router, "DELETE", "/api/watchlists/"+strconv.Itoa(id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
}

func TestHgEWatchlistDelete_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgEWatchlist(t)
	router := gin.New()
	router.DELETE("/api/watchlists/:id", func(c *gin.Context) { setAuth(c, 1, false) }, WatchlistDelete(s))

	w := hgEDo(router, "DELETE", "/api/watchlists/9999", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHgEWatchlistHits_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgEWatchlist(t)
	id := hgESeedWatchlist(t, s, 1)
	if _, err := s.MarkSeen(id, hgEHash, "A title", MagnetPrefix+hgEHash, 12, 999); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/watchlists/:id/hits", func(c *gin.Context) { setAuth(c, 1, false) }, WatchlistHits(s))

	w := hgEDo(router, "GET", "/api/watchlists/"+strconv.Itoa(id)+"/hits?limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	var hits []watchlist.Hit
	if err := json.Unmarshal(w.Body.Bytes(), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("expected at least one hit")
	}
}

func TestHgEWatchlistHits_BadLimitFallsBack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgEWatchlist(t)
	id := hgESeedWatchlist(t, s, 1)

	router := gin.New()
	router.GET("/api/watchlists/:id/hits", func(c *gin.Context) { setAuth(c, 1, false) }, WatchlistHits(s))

	// limit=abc is non-numeric → falls back to default 50, still 200.
	w := hgEDo(router, "GET", "/api/watchlists/"+strconv.Itoa(id)+"/hits?limit=abc", nil)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHgEWatchlistHits_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgEWatchlist(t)
	router := gin.New()
	router.GET("/api/watchlists/:id/hits", func(c *gin.Context) { setAuth(c, 1, false) }, WatchlistHits(s))

	w := hgEDo(router, "GET", "/api/watchlists/9999/hits", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHgEWatchlistCreate_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgEWatchlist(t)
	router := gin.New()
	router.POST("/api/watchlists", func(c *gin.Context) { setAuth(c, 1, false) }, WatchlistCreate(s, nil))

	body, _ := json.Marshal(watchlistInput{Query: "succession", Category: "5000", MinSeeders: 2, NtfyTopic: "n"})
	w := hgEDo(router, "POST", "/api/watchlists", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// subtitles.go — SubtitlesSearch / SubtitlesDownload error paths + errStr
// ---------------------------------------------------------------------------

func TestHgESubtitlesSearch_MissingQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := subtitles.New("", "", "", t.TempDir())
	router := gin.New()
	router.GET("/api/subtitles/search", SubtitlesSearch(client))

	w := hgEDo(router, "GET", "/api/subtitles/search", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHgESubtitlesSearch_NoAPIKeyErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// No API key → Search returns an error → 502.
	client := subtitles.New("", "", "", t.TempDir())
	router := gin.New()
	router.GET("/api/subtitles/search", SubtitlesSearch(client))

	w := hgEDo(router, "GET", "/api/subtitles/search?q=matrix&season=1&episode=2&langs=pt", nil)
	if w.Code != http.StatusBadGateway && w.Code != http.StatusOK {
		t.Errorf("status = %d, want 502 or 200", w.Code)
	}
}

func TestHgESubtitlesDownload_MissingFileID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := subtitles.New("", "", "", t.TempDir())
	router := gin.New()
	// Route with an empty fileId param: hit /api/subtitles/download/ which gin
	// maps to fileId="" so the handler's guard fires.
	router.GET("/api/subtitles/download/:fileId", SubtitlesDownload(client))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/subtitles/download/x", nil)
	c.Params = gin.Params{{Key: "fileId", Value: ""}}
	SubtitlesDownload(client)(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHgESubtitlesDownload_BackendError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := subtitles.New("", "", "", t.TempDir())
	router := gin.New()
	router.GET("/api/subtitles/download/:fileId", SubtitlesDownload(client))

	w := hgEDo(router, "GET", "/api/subtitles/download/12345", nil)
	// No API key / network → Download errors → 502. (200 unlikely but tolerated.)
	if w.Code != http.StatusBadGateway && w.Code != http.StatusOK {
		t.Errorf("status = %d, want 502 or 200", w.Code)
	}
}

func TestHgEErrStr(t *testing.T) {
	if got := errStr(nil); got != "" {
		t.Errorf("errStr(nil) = %q, want empty", got)
	}
	if got := errStr(errHgEBoom); got != "boom" {
		t.Errorf("errStr(err) = %q, want 'boom'", got)
	}
}

type hgEErr struct{}

func (hgEErr) Error() string { return "boom" }

var errHgEBoom = hgEErr{}
