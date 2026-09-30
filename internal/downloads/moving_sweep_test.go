package downloads

import (
	"testing"
)

// Hardening regression: checkCompletion flips a row to `moving` and only then
// submits the transfer job. A crash in between (or a boot rescue that missed)
// wedges the row forever: ListActive only returns `downloading`, so no tick
// ever looks at it again. The worker needs a bounded periodic sweep that
// re-dispatches stuck `moving` rows through the same boot-rescue path.

// A `moving` row with no in-process move running must be re-dispatched
// (flipped back to `downloading`, exactly like rescueInterruptedMove at boot).
func TestSweepStuckMoving_RescuesRowWithoutActiveMove(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), t.TempDir())
	d, err := store.Create(Download{
		UserID: 1, InfoHash: "stuck1", FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:stuck1", Name: "Stuck1", FileSize: 4,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Set AFTER worker construction: NewWorker's boot rescue would otherwise
	// flip it back itself and the test would prove nothing.
	if err := store.SetStatus(1, d.ID, StatusMoving); err != nil {
		t.Fatalf("SetStatus moving: %v", err)
	}

	w.sweepStuckMoving()

	got, _ := store.Get(1, d.ID)
	if got.Status != StatusDownloading {
		t.Fatalf("status = %q, want downloading (stuck move re-dispatched)", got.Status)
	}
}

// A `moving` row whose move goroutine is alive (marked active at dispatch) is a
// healthy long copy: the sweep must leave it alone. Once the move finishes
// (bookkeeping cleared), a later sweep re-dispatches anything still stuck.
func TestSweepStuckMoving_LeavesActiveMoveAlone(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), t.TempDir())
	d, err := store.Create(Download{
		UserID: 1, InfoHash: "stuck2", FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:stuck2", Name: "Stuck2", FileSize: 4,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SetStatus(1, d.ID, StatusMoving); err != nil {
		t.Fatalf("SetStatus moving: %v", err)
	}
	w.markMovingDispatch(d.ID) // checkCompletion's dispatch bookkeeping

	w.sweepStuckMoving()
	got, _ := store.Get(1, d.ID)
	if got.Status != StatusMoving {
		t.Fatalf("status = %q, want moving (active move must not be re-dispatched)", got.Status)
	}

	// The move goroutine exits (success or failure) → bookkeeping cleared.
	w.clearMovingState(d.ID)
	w.sweepStuckMoving()
	got, _ = store.Get(1, d.ID)
	if got.Status != StatusDownloading {
		t.Fatalf("status = %q, want downloading after the move bookkeeping cleared", got.Status)
	}
}

// The sweep is bounded and periodic: it runs every movingSweepTicks ticks from
// the tick loop, not on every tick.
func TestTickSweepRescuesStuckMoving(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), t.TempDir())
	d, err := store.Create(Download{
		UserID: 1, InfoHash: "stuck3", FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:stuck3", Name: "Stuck3", FileSize: 4,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SetStatus(1, d.ID, StatusMoving); err != nil {
		t.Fatalf("SetStatus moving: %v", err)
	}

	// Ticks before the sweep boundary must not touch the row...
	for i := 0; i < movingSweepTicks; i++ {
		w.tick()
		got, _ := store.Get(1, d.ID)
		if i < movingSweepTicks-1 && got.Status != StatusMoving {
			t.Fatalf("status = %q after tick %d, want moving (sweep must be periodic, not per-tick)", got.Status, i+1)
		}
	}
	// ...the Nth tick runs the sweep and re-dispatches the row.
	got, _ := store.Get(1, d.ID)
	if got.Status != StatusDownloading {
		t.Fatalf("status = %q after %d ticks, want downloading (sweep ran)", got.Status, movingSweepTicks)
	}
}
