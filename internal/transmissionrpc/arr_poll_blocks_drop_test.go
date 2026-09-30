package transmissionrpc

import (
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// ─── Regression for the 2026-09-28 incident (*arr angle) ────────────────────
//
// torrent-get (the *arr stack's poll, every ~60s) used streamer.Get, which
// refreshed every active torrent's lastAccess — the drop's activeReadGuard
// stayed permanently armed and every manual "Stop" was silently refused. The
// poll now uses GetUntouched (does not count as use) and the explicit path
// (DropSeed) bypasses the read guard anyway.

// The *arr poll still SEES the torrent — and no longer blocks a manual stop
// issued right after it.
func TestTorrentGet_PollSeesTorrent_AndNoLongerBlocksExplicitStop(t *testing.T) {
	s := streamer.NewForTesting()
	h := NewHandler(nil, s, nil, "/data", "/data", "", nil)

	hash, cleanup := s.SeedActiveForTesting("arr-polled-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

	// The exact path the *arr torrent-get executes every minute.
	active := h.activeTorrentInfo([]downloads.Download{
		{ID: 372, InfoHash: hash.HexString()},
	})
	if _, ok := active[hash.HexString()]; !ok {
		t.Fatal("activeTorrentInfo should resolve the active torrent (that is how the *arr sees it)")
	}

	// Immediately after the poll, the user clicks "Stop".
	if err := s.DropSeed(hash); err != nil {
		t.Fatalf("DropSeed after *arr torrent-get: %v (the poll must no longer arm the guard)", err)
	}
	if n := len(s.ActiveList()); n != 0 {
		t.Fatalf("ActiveList = %d, want 0 — the explicit stop must win", n)
	}
}

// Control: with no poll at all, stopping always worked.
func TestTorrentGet_WithoutPoll_UserDropSucceeds(t *testing.T) {
	s := streamer.NewForTesting()
	_ = NewHandler(nil, s, nil, "/data", "/data", "", nil)

	hash, cleanup := s.SeedActiveForTesting("unpolled-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

	if err := s.DropSeed(hash); err != nil {
		t.Fatalf("DropSeed: %v", err)
	}
	if n := len(s.ActiveList()); n != 0 {
		t.Fatalf("ActiveList = %d, want 0", n)
	}
}

// Fixture sanity: the derived hash resolves in the streamer.
func TestSeedActiveForTesting_HashMatchesTorrent(t *testing.T) {
	s := streamer.NewForTesting()
	hash, cleanup := s.SeedActiveForTesting("hash-check", time.Now())
	defer cleanup()

	var zero metainfo.Hash
	if hash == zero {
		t.Fatal("SeedActiveForTesting returned a zero hash")
	}
	if _, err := s.Get(hash); err != nil {
		t.Fatalf("Get on the seeded torrent: %v", err)
	}
}

// torrent-remove with delete-local-data from the *arr is an EXPLICIT removal:
// even with the read guard armed by a recent poll, the torrent leaves the
// streamer and the row leaves the queue — the same path as "Stop" in the UI.
func TestTorrentRemove_DeleteLocalData_DropsActiveTorrentAfterPoll(t *testing.T) {
	st := newTestStore(t)
	s := streamer.NewForTesting()
	h := NewHandler(st, s, nil, "/data", "/data", "", nil)
	gin.SetMode(gin.ReleaseMode)

	hash, cleanup := s.SeedActiveForTesting("arr-remove-me", time.Now().Add(-2*time.Hour))
	defer cleanup()

	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: hash.HexString(),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + hash.HexString(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// *arr poll immediately before the remove (the incident scenario).
	if got := h.activeTorrentInfo([]downloads.Download{*d}); len(got) != 1 {
		t.Fatalf("activeTorrentInfo = %d entries, want 1", len(got))
	}

	resp := h.methodTorrentRemove(map[string]interface{}{
		"ids":               []interface{}{float64(d.ID)},
		"delete-local-data": true,
	}, sysIdent)
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	if n := len(s.ActiveList()); n != 0 {
		t.Fatalf("ActiveList = %d, want 0 — the explicit *arr remove must drop the torrent", n)
	}
	all, _ := st.ListAll()
	if len(all) != 0 {
		t.Fatalf("expected 0 downloads after remove, got %d", len(all))
	}
}

// Without delete-local-data the *arr only wants the row out of the queue: the
// active torrent (files on disk) must NOT be dropped from the streamer.
func TestTorrentRemove_KeepLocalData_LeavesActiveTorrentAlone(t *testing.T) {
	st := newTestStore(t)
	s := streamer.NewForTesting()
	h := NewHandler(st, s, nil, "/data", "/data", "", nil)
	gin.SetMode(gin.ReleaseMode)

	hash, cleanup := s.SeedActiveForTesting("arr-keep-me", time.Now().Add(-2*time.Hour))
	defer cleanup()

	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: hash.HexString(),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + hash.HexString(),
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := h.methodTorrentRemove(map[string]interface{}{
		"ids": []interface{}{float64(d.ID)},
	}, sysIdent)
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	if n := len(s.ActiveList()); n != 1 {
		t.Fatalf("ActiveList = %d, want 1 — without delete-local-data the torrent stays", n)
	}
	all, _ := st.ListAll()
	if len(all) != 0 {
		t.Fatalf("expected 0 downloads after remove, got %d", len(all))
	}
}
