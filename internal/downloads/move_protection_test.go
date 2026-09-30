package downloads

import (
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// Hardening regression: when the completion move fails for good (after
// moveMaxAttempts), the row is marked failed but the streamer eviction
// protection registered at download start was never released — the torrent
// name stayed protected until the next restart. The error path must release it
// via the sibling-aware unregister helper.
func TestRunCompletionMove_ErrorReleasesEvictionProtection(t *testing.T) {
	store := dlwNewStore(t)
	s := streamer.NewForTesting()
	w := NewWorker(WorkerConfig{
		Store:       store,
		Streamer:    s,
		DataDir:     t.TempDir(), // empty: the move will fail with "completed file not found"
		DownloadDir: t.TempDir(),
		Tracker:     transfer.New(),
	})
	w.moveBackoff = time.Millisecond

	d, err := store.Create(Download{
		UserID: 1, InfoHash: "ghost", FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:ghost", Name: "Ghost", FileSize: 4,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// The move holds eviction protection between the `moving` flip and its own
	// UnregisterDownload (checkCompletion drops the tracked entry without
	// unregistering exactly so the copy survives eviction).
	s.RegisterDownload("Ghost")
	if !s.IsDownloadProtected("Ghost") {
		t.Fatal("precondition: protection must be held while the move runs")
	}

	job := w.tracker.Start("Ghost", "download-move", 1, 4)
	w.runCompletionMove(*d, "Ghost", []string{"Ghost/missing.mkv"}, false, 4, job)

	if s.IsDownloadProtected("Ghost") {
		t.Fatal("failed completion move must release the streamer eviction protection")
	}
	got, _ := store.Get(1, d.ID)
	if got.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
}

// Sibling awareness: when another row of the SAME torrent is still tracked,
// the failed move must NOT release the shared protection (the sibling's
// download would lose its eviction guard).
func TestRunCompletionMove_ErrorKeepsProtectionWhileSiblingTracked(t *testing.T) {
	store := dlwNewStore(t)
	s := streamer.NewForTesting()
	w := NewWorker(WorkerConfig{
		Store:       store,
		Streamer:    s,
		DataDir:     t.TempDir(),
		DownloadDir: t.TempDir(),
		Tracker:     transfer.New(),
	})
	w.moveBackoff = time.Millisecond

	d, err := store.Create(Download{
		UserID: 1, InfoHash: "pack", FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:pack", Name: "Pack", FileSize: 4,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.RegisterDownload("Pack")
	// A sibling of the same torrent is still tracked (mid-download).
	w.mu.Lock()
	w.tracked[d.ID+1] = &trackedDL{id: d.ID + 1, userID: 1, name: "Pack"}
	w.mu.Unlock()
	t.Cleanup(func() {
		w.mu.Lock()
		delete(w.tracked, d.ID+1)
		w.mu.Unlock()
	})

	job := w.tracker.Start("Pack", "download-move", 1, 4)
	w.runCompletionMove(*d, "Pack", []string{"Pack/missing.mkv"}, false, 4, job)

	if !s.IsDownloadProtected("Pack") {
		t.Fatal("protection must stay while a tracked sibling of the same torrent exists")
	}

	// Once the sibling is gone too, a later failure path must release it.
	w.mu.Lock()
	delete(w.tracked, d.ID+1)
	w.mu.Unlock()
	d2, err := store.Create(Download{
		UserID: 1, InfoHash: "pack2", FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:pack2", Name: "Pack", FileSize: 4,
	})
	if err != nil {
		t.Fatalf("Create sibling: %v", err)
	}
	job2 := w.tracker.Start("Pack", "download-move", 1, 4)
	w.runCompletionMove(*d2, "Pack", []string{"Pack/missing.mkv"}, false, 4, job2)
	if s.IsDownloadProtected("Pack") {
		t.Fatal("protection must be released when the last sibling's move fails")
	}
}
