package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func newGoneDownloadsStore(t *testing.T) *downloads.Store {
	t.Helper()
	s, err := downloads.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Queue mutations are user intent that must land even if the client hangs up
// right after sending them (the old ctx-less code did; the ctx refactor must not
// regress that).
func TestDownloadsMutations_SurviveClientDisconnect(t *testing.T) {
	store := newGoneDownloadsStore(t)
	live := context.Background()

	if _, err := store.Create(live, downloads.Download{UserID: 0, InfoHash: "aa", FileIndex: 0, Magnet: "m", Name: "n"}); err != nil {
		t.Fatal(err)
	}
	d, err := store.GetByKey(live, 0, "aa", 0)
	if err != nil {
		t.Fatalf("row missing: %v", err)
	}

	w := serveGone(DownloadsPause(store), "PATCH", "/p/:id", "/p/"+itoa(d.ID), ``)
	if w.Code != http.StatusNoContent {
		t.Fatalf("pause from a gone client = %d (%s)", w.Code, w.Body.String())
	}
	if got, _ := store.Get(live, 0, d.ID); got.Status != downloads.StatusPaused {
		t.Fatalf("pause dropped with the request ctx: status %q", got.Status)
	}

	w = serveGone(DownloadsResume(store), "PATCH", "/r/:id", "/r/"+itoa(d.ID), ``)
	if w.Code != http.StatusNoContent {
		t.Fatalf("resume = %d (%s)", w.Code, w.Body.String())
	}
	if got, _ := store.Get(live, 0, d.ID); got.Status != downloads.StatusQueued {
		t.Fatalf("resume dropped: status %q", got.Status)
	}

	w = serveGone(DownloadsBatchPause(store), "PATCH", "/bp", "/bp", `{"ids":[`+itoa(d.ID)+`]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("batch pause = %d", w.Code)
	}
	if got, _ := store.Get(live, 0, d.ID); got.Status != downloads.StatusPaused {
		t.Fatalf("batch pause dropped: status %q", got.Status)
	}

	w = serveGone(DownloadsDelete(store, nil), "DELETE", "/x/:id", "/x/"+itoa(d.ID), ``)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d (%s)", w.Code, w.Body.String())
	}
	if _, err := store.Get(live, 0, d.ID); err == nil {
		t.Fatal("delete dropped with the request ctx: row survived")
	}
}

// Stop-seed is Get → DropSeed (persisted) → delete row: the three must not be
// split by a disconnect.
func TestDownloadsStopSeed_SurvivesClientDisconnect(t *testing.T) {
	store := newGoneDownloadsStore(t)
	live := context.Background()
	d, err := store.Create(live, downloads.Download{UserID: 0, InfoHash: "aabbccddeeff00112233445566778899aabbccdd", FileIndex: 0, Magnet: "m"})
	if err != nil {
		t.Fatal(err)
	}
	s := streamer.NewForTesting()
	w := serveGone(DownloadsStopSeed(store, s, nil), "POST", "/s/:id", "/s/"+itoa(d.ID), ``)
	if w.Code != http.StatusNoContent {
		t.Fatalf("stop-seed = %d (%s)", w.Code, w.Body.String())
	}
	if _, err := store.Get(live, 0, d.ID); err == nil {
		t.Fatal("row survived a gone-client stop-seed")
	}
}

func TestDownloadsReads_ClientGone_499(t *testing.T) {
	store := newGoneDownloadsStore(t)
	w := serveGone(DownloadsTrackers(store), "GET", "/t", "/t", ``)
	if w.Code != 499 || w.Body.Len() != 0 {
		t.Errorf("trackers: %d %q; want bodiless 499", w.Code, w.Body.String())
	}
	w = serveGone(DownloadsCategories(store), "GET", "/c", "/c", ``)
	if w.Code != 499 || w.Body.Len() != 0 {
		t.Errorf("categories: %d %q; want bodiless 499", w.Code, w.Body.String())
	}
}
