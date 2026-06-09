package downloads

import (
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/streamer"
)

func newQueueWorker(t *testing.T, store *Store, qs QueueSettings) *Worker {
	t.Helper()
	return NewWorker(WorkerConfig{
		Store:    store,
		Streamer: streamer.NewForTesting(),
		DataDir:  t.TempDir(),
		Settings: func() QueueSettings { return qs },
	})
}

func TestApplySchedule_PromotesUpToMaxActive(t *testing.T) {
	store := newTestStore(t)
	for i := 0; i < 5; i++ {
		_, _ = store.Create(Download{UserID: 1, InfoHash: string(rune('a' + i)), FileIndex: 0, Magnet: "m", Name: "d"})
	}
	w := newQueueWorker(t, store, QueueSettings{MaxActive: 2})

	w.applySchedule(w.queueSettings())

	sched, _ := store.ListSchedulable()
	downloading := 0
	for _, d := range sched {
		if d.Status == StatusDownloading {
			downloading++
		}
	}
	if downloading != 2 {
		t.Fatalf("expected 2 promoted to downloading, got %d", downloading)
	}
}

func TestApplySchedule_PreemptsLowerPriorityOverLimit(t *testing.T) {
	store := newTestStore(t)
	low, _ := store.Create(Download{UserID: 1, InfoHash: "low", FileIndex: 0, Magnet: "m", Name: "low", Priority: PriorityLow})
	_, _ = store.PromoteToDownloading(low.ID) // simulate it already running
	high, _ := store.Create(Download{UserID: 1, InfoHash: "high", FileIndex: 0, Magnet: "m", Name: "high", Priority: PriorityHigh})

	w := newQueueWorker(t, store, QueueSettings{MaxActive: 1})
	w.applySchedule(w.queueSettings())

	gotLow, _ := store.Get(1, low.ID)
	gotHigh, _ := store.Get(1, high.ID)
	if gotHigh.Status != StatusDownloading {
		t.Errorf("high should be promoted, got %q", gotHigh.Status)
	}
	if gotLow.Status != StatusQueued {
		t.Errorf("low should be preempted back to queued, got %q", gotLow.Status)
	}
	// Preemption must NOT count as a stall.
	if gotLow.Stalls != 0 {
		t.Errorf("preemption should not bump stalls, got %d", gotLow.Stalls)
	}
}

func TestApplySchedule_NoOpWhenUnderLimit(t *testing.T) {
	store := newTestStore(t)
	a, _ := store.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a"})
	w := newQueueWorker(t, store, QueueSettings{MaxActive: 3})
	w.applySchedule(w.queueSettings())
	got, _ := store.Get(1, a.ID)
	if got.Status != StatusDownloading {
		t.Fatalf("single queued under limit should be promoted, got %q", got.Status)
	}
}

func TestQueueSettings_FallsBackToDefaults(t *testing.T) {
	store := newTestStore(t)
	// No Settings getter wired → defaults.
	w := NewWorker(WorkerConfig{Store: store, Streamer: streamer.NewForTesting(), DataDir: t.TempDir()})
	qs := w.queueSettings()
	def := DefaultQueueSettings()
	if qs != def {
		t.Fatalf("expected defaults %+v, got %+v", def, qs)
	}

	// Getter returning zero MaxActive → also defaults (guards a misconfigured 0).
	w2 := newQueueWorker(t, store, QueueSettings{MaxActive: 0})
	if w2.queueSettings() != def {
		t.Fatalf("zero MaxActive should fall back to defaults")
	}

	// Valid getter is honored.
	custom := QueueSettings{MaxActive: 5, StallThresholdMin: 10, MaxStalls: 2, AgingStepMin: 30, AgingCap: 100}
	w3 := newQueueWorker(t, store, custom)
	if w3.queueSettings() != custom {
		t.Fatalf("expected custom settings honored, got %+v", w3.queueSettings())
	}
}

func TestDetectStalls_DisabledWhenThresholdZero(t *testing.T) {
	store := newTestStore(t)
	w := newQueueWorker(t, store, QueueSettings{MaxActive: 3, StallThresholdMin: 0})
	// Should be a no-op (and not panic) with no tracked downloads.
	w.detectStalls(w.queueSettings())
}

func TestDetectStalls_DemotesThenPausesAtMaxStalls(t *testing.T) {
	store := newTestStore(t)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A"})
	_, _ = store.PromoteToDownloading(d.ID)
	w := newQueueWorker(t, store, QueueSettings{MaxActive: 3, StallThresholdMin: 30, MaxStalls: 2})

	// Inject a tracked download with stale progress and a nil torrent (nil torrent
	// ⇒ the "has seeders" guard is skipped ⇒ treated as a no-seed stall).
	stale := time.Now().Add(-time.Hour)
	inject := func() {
		w.mu.Lock()
		w.tracked[d.ID] = &trackedDL{id: d.ID, userID: 1, name: "A", lastProgressAt: stale}
		w.mu.Unlock()
	}

	inject()
	w.detectStalls(w.queueSettings())
	got, _ := store.Get(1, d.ID)
	if got.Status != StatusQueued || got.Stalls != 1 {
		t.Fatalf("after 1st stall: status=%q stalls=%d, want queued/1", got.Status, got.Stalls)
	}
	w.mu.Lock()
	_, stillTracked := w.tracked[d.ID]
	w.mu.Unlock()
	if stillTracked {
		t.Error("tracked entry should be removed after demote")
	}

	// Second stall reaches MaxStalls=2 → paused (not failed, per the user's choice).
	_, _ = store.PromoteToDownloading(d.ID)
	inject()
	w.detectStalls(w.queueSettings())
	got, _ = store.Get(1, d.ID)
	if got.Status != StatusPaused {
		t.Fatalf("after reaching MaxStalls: status=%q, want paused", got.Status)
	}
}

func TestDetectStalls_SkipsRecentlyProgressed(t *testing.T) {
	store := newTestStore(t)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A"})
	_, _ = store.PromoteToDownloading(d.ID)
	w := newQueueWorker(t, store, QueueSettings{MaxActive: 3, StallThresholdMin: 30, MaxStalls: 3})

	// Progressed just now → must NOT be demoted.
	w.mu.Lock()
	w.tracked[d.ID] = &trackedDL{id: d.ID, userID: 1, name: "A", lastProgressAt: time.Now()}
	w.mu.Unlock()
	w.detectStalls(w.queueSettings())
	got, _ := store.Get(1, d.ID)
	if got.Status != StatusDownloading {
		t.Fatalf("recently-progressed download should stay downloading, got %q", got.Status)
	}
}

func TestPreemptActive_TearsDownTracking(t *testing.T) {
	store := newTestStore(t)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A"})
	_, _ = store.PromoteToDownloading(d.ID)
	w := newQueueWorker(t, store, QueueSettings{MaxActive: 3})

	cancelled := false
	w.mu.Lock()
	w.tracked[d.ID] = &trackedDL{id: d.ID, userID: 1, name: "A"}
	w.pending[d.ID] = func() { cancelled = true }
	w.retries[d.ID] = 2
	w.mu.Unlock()

	got, _ := store.Get(1, d.ID)
	w.preemptActive(*got)

	gotAfter, _ := store.Get(1, d.ID)
	if gotAfter.Status != StatusQueued {
		t.Errorf("preempted row should be queued, got %q", gotAfter.Status)
	}
	if gotAfter.Stalls != 0 {
		t.Errorf("preemption must not count a stall, got %d", gotAfter.Stalls)
	}
	w.mu.Lock()
	_, tracked := w.tracked[d.ID]
	_, pending := w.pending[d.ID]
	_, retried := w.retries[d.ID]
	w.mu.Unlock()
	if tracked || pending || retried {
		t.Errorf("tracking not torn down: tracked=%v pending=%v retried=%v", tracked, pending, retried)
	}
	if !cancelled {
		t.Error("pending init should have been cancelled")
	}
}

func TestUnregisterLocked_KeepsSiblingProtection(t *testing.T) {
	store := newTestStore(t)
	w := newQueueWorker(t, store, QueueSettings{MaxActive: 3})
	// Two tracked downloads sharing the same torrent name (2 files, 1 torrent).
	td1 := &trackedDL{id: 1, name: "Shared.Torrent"}
	td2 := &trackedDL{id: 2, name: "Shared.Torrent"}
	w.mu.Lock()
	w.tracked[1] = td1
	w.tracked[2] = td2
	// Removing #1 must not unregister while #2 still shares the name.
	delete(w.tracked, 1)
	w.unregisterLocked(td1) // sibling #2 present → no-op (just must not panic)
	w.mu.Unlock()
}
