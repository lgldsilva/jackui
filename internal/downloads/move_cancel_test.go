package downloads

import (
	"strings"
	"testing"

	"github.com/lgldsilva/jackui/internal/transfer"
)

// canceledJob returns a tracked job already flipped to canceled, the way
// Tracker.Cancel (the Transfers dock "X") leaves it for the copy loop to see.
func canceledJob(t *testing.T) *transfer.Job {
	t.Helper()
	tr := transfer.New()
	job := tr.Start("pack", "download-move", 1, 1)
	if !tr.Cancel(job.ID(), 0, true) {
		t.Fatal("Cancel should succeed on a running job")
	}
	if !job.Canceled() {
		t.Fatal("job should report canceled")
	}
	return job
}

// A canceled job aborts the single-file completion move BEFORE any attempt
// (no retries, no backoff) with the sentinel error.
func TestAttemptCompletionMove_CanceledJob(t *testing.T) {
	w, store := newBulkWorker(t, t.TempDir(), "", false)
	d, err := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "m", Name: "Gone.mkv"})
	if err != nil {
		t.Fatal(err)
	}

	dst, err := w.attemptCompletionMove(*d, "Gone.mkv", []string{"Gone.mkv"}, false, canceledJob(t))
	if err == nil || !strings.Contains(err.Error(), "transfer canceled") {
		t.Fatalf("err = %v, want transfer-canceled", err)
	}
	if dst != "" {
		t.Errorf("dst = %q, want empty on cancel", dst)
	}
}

// Same guard inside the per-file loop of a multi-file group move.
func TestMoveGroupFiles_CanceledJob(t *testing.T) {
	w, store := newBulkWorker(t, t.TempDir(), "", false)
	d, err := store.Create(Download{UserID: 1, InfoHash: "grp", FileIndex: 0, Magnet: "m", Name: "Pack"})
	if err != nil {
		t.Fatal(err)
	}
	g := Group{Key: "1:grp", UserID: 1, Members: []Download{*d}}
	movers := []groupMover{{row: *d, relPath: "Pack/a.bin", length: 1}}

	err = w.moveGroupFiles(g, "Pack", movers, canceledJob(t))
	if err == nil || !strings.Contains(err.Error(), "transfer canceled") {
		t.Fatalf("err = %v, want transfer-canceled", err)
	}
}

// runGroupCompletionMove on a canceled job: no retry loop, every member goes
// `failed` with the cancel reason and the job is marked failed.
func TestRunGroupCompletionMove_CanceledJob(t *testing.T) {
	w, store := newBulkWorker(t, t.TempDir(), "", false)
	d, err := store.Create(Download{UserID: 1, InfoHash: "grp2", FileIndex: 0, Magnet: "m", Name: "Pack"})
	if err != nil {
		t.Fatal(err)
	}
	g := Group{Key: "1:grp2", UserID: 1, Members: []Download{*d}}
	movers := []groupMover{{row: *d, relPath: "Pack/a.bin", length: 1}}
	job := canceledJob(t)

	w.runGroupCompletionMove(g, "Pack", movers, 1, job)

	row, err := store.Get(1, d.ID)
	if err != nil || row == nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Status != StatusFailed || !strings.Contains(row.Error, "transfer canceled") {
		t.Errorf("row = status %q error %q, want failed/transfer canceled", row.Status, row.Error)
	}
}
