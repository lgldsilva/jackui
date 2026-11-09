package downloads

import (
	"context"
	"testing"

	"github.com/lgldsilva/jackui/internal/streamer"
)

func genTestWorker(t *testing.T) *Worker {
	t.Helper()
	return NewWorker(WorkerConfig{Store: newTestStore(t), Streamer: streamer.NewForTesting(), DataDir: t.TempDir()})
}

// registerInit mimics startInit's bookkeeping and returns the init's ctx.
func registerInit(w *Worker, id int) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	gen := w.setPendingLocked(id, "", cancel)
	w.mu.Unlock()
	return withInitGen(ctx, gen)
}

// TestStaleInitExitKeepsNewerPending: init A is cancelled (preempt clears its
// entry), the row is rescheduled and init B registers under the same id; A's
// late exit must not wipe B's entry, promote over it, or count a retry for it.
func TestStaleInitExitKeepsNewerPending(t *testing.T) {
	w := genTestWorker(t)
	const id = 7
	ctxA := registerInit(w, id)
	w.mu.Lock()
	w.clearPendingLocked(id) // preemptTeardown
	w.mu.Unlock()
	ctxB := registerInit(w, id)

	// A fails late: must not touch B's retry budget.
	w.failOrRetry(ctxA, Download{ID: id}, "stale failure")
	// A's promotion guard must refuse (B owns the slot).
	if w.promoteOrAbort(ctxA, Download{ID: id}, &trackedDL{id: id}, "n") {
		t.Fatal("stale init must not promote over the newer init")
	}
	// A's deferred exit cleanup.
	w.mu.Lock()
	w.releasePendingLocked(id, initGen(ctxA))
	_, stillPending := w.pending[id]
	retries := w.retries[id]
	_, tracked := w.tracked[id]
	w.mu.Unlock()
	if !stillPending {
		t.Fatal("stale init exit cleared the newer init's pending entry")
	}
	if retries != 0 || tracked {
		t.Fatalf("stale init leaked into B: retries=%d tracked=%v", retries, tracked)
	}

	// B, the owner, still works end to end.
	if !w.stillInitOwner(ctxB, id) {
		t.Fatal("B must still own the slot")
	}
	w.mu.Lock()
	w.releasePendingLocked(id, initGen(ctxB))
	_, stillPending = w.pending[id]
	w.mu.Unlock()
	if stillPending {
		t.Fatal("owner exit must clear its entry")
	}
}

// TestCancelledInitExitClearsTombstone: Remove cancels + clears the entry and
// leaves a tombstone; with nothing newer registered the init's exit clears it.
func TestCancelledInitExitClearsTombstone(t *testing.T) {
	w := genTestWorker(t)
	ctx := registerInit(w, 3)
	w.mu.Lock()
	w.removed[3] = struct{}{}
	w.clearPendingLocked(3)
	w.releasePendingLocked(3, initGen(ctx))
	_, tomb := w.removed[3]
	w.mu.Unlock()
	if tomb {
		t.Fatal("tombstone leaked after the cancelled init exited")
	}
}

func TestStillInitOwnerFalseAfterCancel(t *testing.T) {
	w := genTestWorker(t)
	ctx, cancel := context.WithCancel(registerInit(w, 4))
	cancel()
	if w.stillInitOwner(ctx, 4) {
		t.Fatal("a cancelled init no longer owns its members")
	}
}
