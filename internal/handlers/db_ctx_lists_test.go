package handlers

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/playlists"
	"github.com/lgldsilva/jackui/internal/watchlist"
)

// The read handlers must pass the request ctx to the store: a dead client then
// yields a bodiless 499 instead of data (detached ctx) or a leaked driver 500.
func TestPlaylistAndWatchlistReads_ClientGoneAborts499(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pl, err := playlists.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	wl, err := watchlist.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	created, err := pl.Create(context.Background(), 1, "p", "")
	if err != nil {
		t.Fatal(err)
	}
	w1, err := wl.Create(context.Background(), 1, watchlist.Params{Query: "q"})
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]gin.HandlerFunc{
		"PlaylistsList": PlaylistsList(pl),
		"PlaylistsGet":  PlaylistsGet(pl),
		"WatchlistList": WatchlistList(wl),
		"WatchlistHits": WatchlistHits(wl),
	}
	ids := map[string]int{"PlaylistsGet": created.ID, "WatchlistHits": w1.ID}
	for name, h := range cases {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/x", nil).WithContext(ctx)
		if id, ok := ids[name]; ok {
			c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(id)}}
		}
		setAuth(c, 1, false)
		h(c)
		if w.Code != 499 || w.Body.String() != "" {
			t.Errorf("%s: status/body = %d %q, want bodiless 499", name, w.Code, w.Body.String())
		}
	}
}
