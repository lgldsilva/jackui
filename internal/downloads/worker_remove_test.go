package downloads

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/streamer"
)

// hashFromHex is a test helper: a deterministic non-zero metainfo.Hash from a
// 40-char hex string.
func hashFromHex(t *testing.T, hex string) metainfo.Hash {
	t.Helper()
	var h metainfo.Hash
	if err := h.FromHexString(hex); err != nil {
		t.Fatalf("FromHexString(%q): %v", hex, err)
	}
	return h
}

const testHashHex = "0123456789abcdef0123456789abcdef01234567"

// newRemoveWorker builds a worker with an injected drop seam so we can assert
// the torrent was dropped without a live anacrolix client.
func newRemoveWorker(t *testing.T) (*Worker, *Store, *dropRecorder) {
	t.Helper()
	s := streamer.NewForTesting()
	store := newTestStore(t)
	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
		Interval: time.Hour, // never auto-tick; tests drive Remove directly
	})
	rec := &dropRecorder{}
	w.drop = rec.drop
	return w, store, rec
}

type dropRecorder struct {
	mu      sync.Mutex
	dropped []metainfo.Hash
}

func (r *dropRecorder) drop(h metainfo.Hash) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropped = append(r.dropped, h)
}

func (r *dropRecorder) calls() []metainfo.Hash {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]metainfo.Hash, len(r.dropped))
	copy(out, r.dropped)
	return out
}

// Remove tears down a TRACKED download: clears tracked/retries, drops the
// torrent by its hash, and unregisters the streamer's eviction protection.
func TestWorkerRemove_TrackedDropsTorrentAndUntracks(t *testing.T) {
	w, store, rec := newRemoveWorker(t)
	d, err := store.Create(Download{UserID: 1, InfoHash: testHashHex, FileIndex: 0, Magnet: "m", Name: "Movie"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h := hashFromHex(t, testHashHex)
	w.streamer.RegisterDownload("Movie")
	w.mu.Lock()
	w.tracked[d.ID] = &trackedDL{id: d.ID, name: "Movie", hash: h}
	w.retries[d.ID] = 2
	w.mu.Unlock()

	w.Remove(d.ID, testHashHex)

	w.mu.Lock()
	_, stillTracked := w.tracked[d.ID]
	_, stillRetries := w.retries[d.ID]
	w.mu.Unlock()
	if stillTracked {
		t.Error("tracked entry must be gone after Remove")
	}
	if stillRetries {
		t.Error("retries entry must be gone after Remove")
	}
	if w.streamer.IsDownloadProtected("Movie") {
		t.Error("streamer protection must be unregistered after Remove")
	}
	calls := rec.calls()
	if len(calls) != 1 || calls[0] != h {
		t.Errorf("expected exactly one Drop of %v, got %v", h, calls)
	}
}

// Remove of a QUEUED row (never tracked) still drops the torrent using the
// persisted infoHash — a queued/initializing row may have an active torrent in
// the streamer even though the worker hasn't promoted it.
func TestWorkerRemove_UntrackedDropsByInfoHash(t *testing.T) {
	w, _, rec := newRemoveWorker(t)
	h := hashFromHex(t, testHashHex)

	w.Remove(42, testHashHex)

	calls := rec.calls()
	if len(calls) != 1 || calls[0] != h {
		t.Errorf("expected Drop of %v even when untracked, got %v", h, calls)
	}
}

// Remove cancels an in-flight init goroutine (pending) and records a tombstone.
func TestWorkerRemove_CancelsPendingAndSetsTombstone(t *testing.T) {
	w, _, _ := newRemoveWorker(t)
	cancelled := false
	w.mu.Lock()
	w.pending[7] = func() { cancelled = true }
	w.mu.Unlock()

	w.Remove(7, "")

	w.mu.Lock()
	_, stillPending := w.pending[7]
	_, tombstoned := w.removed[7]
	w.mu.Unlock()
	if !cancelled {
		t.Error("Remove must cancel the in-flight init")
	}
	if stillPending {
		t.Error("pending entry must be cleared")
	}
	if !tombstoned {
		t.Error("Remove must record a deletion tombstone")
	}
}

// Remove with no hash at all (empty infoHash, never tracked) is a safe no-op
// for Drop but still records the tombstone.
func TestWorkerRemove_NoHashNoDrop(t *testing.T) {
	w, _, rec := newRemoveWorker(t)

	w.Remove(99, "")

	if calls := rec.calls(); len(calls) != 0 {
		t.Errorf("expected no Drop when no hash is known, got %v", calls)
	}
	w.mu.Lock()
	_, tombstoned := w.removed[99]
	w.mu.Unlock()
	if !tombstoned {
		t.Error("tombstone must be recorded even with no hash")
	}
}

// A late init result (the init goroutine finished resolving metadata just as
// the delete landed) must NOT re-promote the deleted row. The promotion guard
// checks both the missing `pending` entry AND the tombstone — here we simulate
// the worst case: the tombstone is set but a stale `pending` entry still
// exists, so ONLY the tombstone protects against resurrection.
func TestWorkerInit_TombstonedRowNotPromoted(t *testing.T) {
	w, store, _ := newRemoveWorker(t)
	d, err := store.Create(Download{UserID: 1, InfoHash: testHashHex, FileIndex: 0, Magnet: "m", Name: "Late"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// The row was deleted while init was resolving metadata: tombstone is set.
	// Re-add a pending entry to isolate the tombstone as the sole guard.
	w.mu.Lock()
	w.removed[d.ID] = struct{}{}
	w.pending[d.ID] = func() {}
	w.mu.Unlock()

	// Drive the promotion path the way initDownload does after GotInfo.
	w.streamer.RegisterDownload("Late")
	td := &trackedDL{id: d.ID, name: "Late", hash: hashFromHex(t, testHashHex)}
	promoted := w.promoteOrAbort(*d, td, "Late")

	if promoted {
		t.Fatal("a tombstoned (deleted) row must NOT be promoted by a late init")
	}
	w.mu.Lock()
	_, stillTracked := w.tracked[d.ID]
	w.mu.Unlock()
	if stillTracked {
		t.Error("tombstoned row must not appear in tracked")
	}
	if w.streamer.IsDownloadProtected("Late") {
		t.Error("late init must unregister the protection it speculatively registered")
	}
}

// A normal (non-deleted) row IS promoted by promoteOrAbort.
func TestWorkerInit_LivePromoted(t *testing.T) {
	w, store, _ := newRemoveWorker(t)
	d, err := store.Create(Download{UserID: 1, InfoHash: testHashHex, FileIndex: 0, Magnet: "m", Name: "Live"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	w.mu.Lock()
	w.pending[d.ID] = func() {}
	w.mu.Unlock()

	td := &trackedDL{id: d.ID, name: "Live", hash: hashFromHex(t, testHashHex)}
	if !w.promoteOrAbort(*d, td, "Live") {
		t.Fatal("a live (non-deleted) row must be promoted")
	}
	w.mu.Lock()
	_, tracked := w.tracked[d.ID]
	w.mu.Unlock()
	if !tracked {
		t.Error("promoted row must appear in tracked")
	}
}

// End-to-end via the worker maps: a download tracked by the worker, removed,
// then a tick reconcile must not resurrect it (tombstone + store row gone).
func TestWorkerRemove_ReconcileDoesNotResurrect(t *testing.T) {
	w, store, _ := newRemoveWorker(t)
	d, err := store.Create(Download{UserID: 1, InfoHash: testHashHex, FileIndex: 0, Magnet: "m", Name: "Gone"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	w.mu.Lock()
	w.tracked[d.ID] = &trackedDL{id: d.ID, name: "Gone", hash: hashFromHex(t, testHashHex)}
	w.mu.Unlock()

	// Authoritative delete: store + worker.
	if _, err := store.DeleteScoped(1, d.ID, false); err != nil {
		t.Fatalf("DeleteScoped: %v", err)
	}
	w.Remove(d.ID, testHashHex)

	// A reconcile of the (now stale) Download value must not re-track it: with
	// pending cleared and no live torrent, startInit launches an init, but that
	// init will bail (tombstone). We just assert no synchronous re-track.
	w.mu.Lock()
	_, tracked := w.tracked[d.ID]
	w.mu.Unlock()
	if tracked {
		t.Error("removed download must not be tracked")
	}
	// The store row is gone — a tick's ListActive won't return it.
	if got, _ := store.Get(1, d.ID); got != nil {
		t.Error("store row must be gone after DeleteScoped")
	}
}

// Stop must not deadlock when a tombstone is present (sanity for the lock
// ordering in Remove vs Stop).
func TestWorkerRemove_StopAfterRemove(t *testing.T) {
	w, store, _ := newRemoveWorker(t)
	w.interval = 20 * time.Millisecond
	d, _ := store.Create(Download{UserID: 1, InfoHash: testHashHex, FileIndex: 0, Magnet: "m", Name: "x"})
	w.Remove(d.ID, testHashHex)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		w.Start()
		w.Stop() // joins run() after its bootstrap tick; exercises the lock ordering
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("Start/Stop deadlocked after Remove")
	}
}
