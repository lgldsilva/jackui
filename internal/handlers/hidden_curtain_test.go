package handlers

import (
	"encoding/base64"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// newCurtainStreamer builds a test streamer with a real favourites store.
func newCurtainStreamer(t *testing.T) (*streamer.Streamer, *streamer.FavoritesStore) {
	t.Helper()
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(func() { fav.Close() })
	s.SetFavorites(fav)
	return s, fav
}

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
}

func TestParseLocalInfoHashAndDropLocalLibrary(t *testing.T) {
	// Build the same encoding localInfoHash uses (JSON without HTML escape).
	raw := []byte(`{"mount":"Movies","path":"secret/movie.mkv"}`)
	hash := "local-" + base64.RawURLEncoding.EncodeToString(raw)
	mount, path, ok := parseLocalInfoHash(hash)
	if !ok || mount != "Movies" || path != "secret/movie.mkv" {
		t.Fatalf("parse = %q %q ok=%v", mount, path, ok)
	}
	if _, _, ok := parseLocalInfoHash("abcdef0123456789"); ok {
		t.Fatal("torrent hash must not parse as local")
	}
	byMount := map[string]map[string]bool{"Movies": {"secret": true}}
	if !localLibraryEntryHidden(hash, byMount) {
		t.Fatal("file under hidden folder must be hidden")
	}
	pub := "local-" + base64.RawURLEncoding.EncodeToString([]byte(`{"mount":"Movies","path":"public/x.mkv"}`))
	if localLibraryEntryHidden(pub, byMount) {
		t.Fatal("public local path must not be hidden")
	}
}

func TestDropHiddenLocalLibrary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, fav := newCurtainStreamer(t)
	const uid = 4
	if err := fav.SetLocalPathHidden(uid, "Movies", "secret", true); err != nil {
		t.Fatalf("hide: %v", err)
	}
	secretHash := "local-" + base64.RawURLEncoding.EncodeToString([]byte(`{"mount":"Movies","path":"secret/a.mkv"}`))
	pubHash := "local-" + base64.RawURLEncoding.EncodeToString([]byte(`{"mount":"Movies","path":"pub/a.mkv"}`))
	list := []library.Entry{
		{InfoHash: secretHash, Name: "secret"},
		{InfoHash: pubHash, Name: "pub"},
		{InfoHash: "deadbeef", Name: "torrent"},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/library", nil)
	got := dropHiddenLocalLibrary(c, s, list, uid)
	if len(got) != 2 {
		t.Fatalf("closed curtain: want 2 (pub+torrent), got %d %+v", len(got), got)
	}
	for _, e := range got {
		if e.InfoHash == secretHash {
			t.Fatal("secret local entry leaked")
		}
	}

	// Curtain open → secret reappears.
	c.Set("reveal_hidden", true)
	if !middleware.IsRevealHidden(c) {
		t.Fatal("setup: reveal flag not set")
	}
	gotOpen := dropHiddenLocalLibrary(c, s, list, uid)
	if len(gotOpen) != 3 {
		t.Fatalf("open curtain: want 3, got %d", len(gotOpen))
	}
}
