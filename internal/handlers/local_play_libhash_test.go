package handlers

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/lgldsilva/jackui/internal/library"
)

// The backend local pseudo-hash MUST byte-for-byte match the frontend's
// buildLocalHash (web/src/api/client.ts) — the deep-link ?play=local-… and the
// library row have to agree for Continue Watching + resume to find the entry.
func TestLocalInfoHashMatchesFrontend(t *testing.T) {
	h := localInfoHash("Mia Biblioteca", "Audios/music.mp3")
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(h, "local-"))
	if err != nil || string(raw) != `{"mount":"Mia Biblioteca","path":"Audios/music.mp3"}` {
		t.Fatalf("hash json mismatch: %q (err %v)", raw, err)
	}
	// '&' must stay literal — JS JSON.stringify does NOT HTML-escape it, so Go
	// must disable its default escaping or the hashes diverge.
	h2 := localInfoHash("M", "Simon & Garfunkel.flac")
	raw2, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(h2, "local-"))
	if string(raw2) != `{"mount":"M","path":"Simon & Garfunkel.flac"}` {
		t.Fatalf("& was escaped (would break the deep-link match): %q", raw2)
	}
}

func TestUpsertLocalLibrary(t *testing.T) {
	lib, err := library.New(t.TempDir() + "/lib.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lib.Close)
	c, _ := newTestCtx()
	id := upsertLocalLibrary(c, lib, "M", "song.flac", true)
	if id <= 0 {
		t.Fatalf("expected a library row id > 0, got %d", id)
	}
	if got := upsertLocalLibrary(c, nil, "M", "song.flac", true); got != 0 {
		t.Errorf("nil store must return 0, got %d", got)
	}
}
