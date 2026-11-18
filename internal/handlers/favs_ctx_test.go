package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/streamer"
)

// goneReq builds a request whose client has already disconnected.
func goneReq(method, path, body string) *http.Request {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	return r
}

func serveGone(h gin.HandlerFunc, method, route, path, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Handle(method, route, h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, goneReq(method, path, body))
	return w
}

// Favourite writes are the LRU shield and the user's folder organisation: they
// must land even when the client hangs up right after sending them.
func TestFavoriteWrites_SurviveClientDisconnect(t *testing.T) {
	s := newStreamerWithFavs(t)
	favs := s.Favorites()
	live := context.Background()

	w := serveGone(StreamFavorite(s), "POST", "/f", "/f", `{"name":"keep","infoHash":"","magnet":"m"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("favorite from a gone client = %d (%s); want 200", w.Code, w.Body.String())
	}
	if !favs.IsFavorite(live, "keep") {
		t.Fatal("favourite was dropped with the request ctx")
	}

	fl, err := favs.CreateFolder(live, 0, "F", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	// Rename + hide are paired in one patch: both or neither.
	w = serveGone(FolderPatch(s), "PATCH", "/f/:id", "/f/"+itoa(fl.ID), `{"name":"R","hidden":true}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("patch = %d (%s)", w.Code, w.Body.String())
	}
	got, err := favs.GetFolder(live, 0, fl.ID)
	if err != nil || got.Name != "R" || !got.Hidden {
		t.Fatalf("folder after gone-client patch = %+v, %v; want renamed AND hidden", got, err)
	}

	w = serveGone(FavoriteMoveToFolder(s), "PUT", "/m/:name", "/m/keep", `{"folderId":`+itoa(fl.ID)+`}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("move = %d (%s)", w.Code, w.Body.String())
	}
	list, _ := favs.List(live, 0, false, true)
	if len(list) != 1 || list[0].FolderID == nil || *list[0].FolderID != fl.ID {
		t.Fatalf("favourite not moved: %+v", list)
	}

	w = serveGone(FavoritesBatchRemove(s), "POST", "/b", "/b", `{"names":["keep"]}`)
	if w.Code != http.StatusOK || favs.IsFavorite(live, "keep") {
		t.Fatalf("batch remove from a gone client = %d, still favourite=%v", w.Code, favs.IsFavorite(live, "keep"))
	}

	w = serveGone(FolderCreate(s), "POST", "/c", "/c", `{"name":"New"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create folder = %d (%s)", w.Code, w.Body.String())
	}
	w = serveGone(FolderDelete(s), "DELETE", "/d/:id", "/d/"+itoa(fl.ID), ``)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete folder = %d", w.Code)
	}
	if _, err := favs.GetFolder(live, 0, fl.ID); err == nil {
		t.Fatal("folder survived a gone-client delete")
	}
}

func TestFavoriteUnfavorite_SurvivesClientDisconnect(t *testing.T) {
	s := newStreamerWithFavs(t)
	live := context.Background()
	if err := s.Favorites().Add(live, "x", "", "m", "manual", 0); err != nil {
		t.Fatal(err)
	}
	w := serveGone(StreamUnfavorite(s), "DELETE", "/u/:name", "/u/x", ``)
	if w.Code != http.StatusOK || s.Favorites().IsFavorite(live, "x") {
		t.Fatalf("unfavorite = %d, still favourite=%v", w.Code, s.Favorites().IsFavorite(live, "x"))
	}
}

// GET handlers on a dead client answer a bodiless 499, not a misleading 404/500
// or an "empty" body a cache could keep.
func TestFavoriteReads_ClientGone_499(t *testing.T) {
	s := newStreamerWithFavs(t)
	mc, err := streamer.NewMetadataCache(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	s.SetMetadataCache(mc)
	_ = mc.Set(context.Background(), &streamer.TorrentInfo{InfoHash: "aabbccddeeff00112233445566778899aabbccdd", Name: "n"})
	for name, tc := range map[string]struct {
		h           gin.HandlerFunc
		route, path string
	}{
		"favorites": {StreamFavorites(s), "/l", "/l"},
		"folders":   {FoldersList(s), "/fl", "/fl"},
		"metadata":  {StreamMetadata(s), "/m/:hash", "/m/aabbccddeeff00112233445566778899aabbccdd"},
	} {
		w := serveGone(tc.h, "GET", tc.route, tc.path, ``)
		if w.Code != 499 || w.Body.Len() != 0 {
			t.Errorf("%s: status %d body %q; want bodiless 499", name, w.Code, w.Body.String())
		}
	}
}

// A resolved thumbnail is persisted on a ctx detached from the resolve chain's
// deadline/request, so a budget that ran out an instant later cannot lose it.
func TestPersistArt_SurvivesDoneParentContext(t *testing.T) {
	mc, err := streamer.NewMetadataCache(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	persistArt(ctx, mc, "aabb", &streamer.CachedArt{Source: "tmdb", PosterURL: "https://img/x.jpg"})
	got := mc.GetArt(context.Background(), "aabb")
	if got == nil || got.Source != "tmdb" {
		t.Fatalf("art lost with a done parent ctx: %+v", got)
	}
}
