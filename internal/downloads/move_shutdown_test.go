package downloads

import (
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// Regression: a completion move interrupted by shutdown (w.stop closed during
// the retry backoff) used to take the generic error path — row marked `failed`,
// eviction protection released, transfer job failed. registerExistingDownloads
// skips `failed` rows at boot, so the interrupted move was never rescued. The
// row must stay `moving`, exactly like the group path does.
func TestRunCompletionMove_ShutdownDuringBackoffKeepsRowMoving(t *testing.T) {
	store := dlwNewStore(t)
	s := streamer.NewForTesting()
	w := NewWorker(WorkerConfig{
		Store:       store,
		Streamer:    s,
		DataDir:     t.TempDir(), // empty: every attempt fails with "completed file not found"
		DownloadDir: t.TempDir(),
		Tracker:     transfer.New(),
	})
	// Long enough that the only way out of the backoff is the stop signal.
	w.moveBackoff = time.Hour

	d, err := store.Create(Download{
		UserID: 1, InfoHash: "halt", FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:halt", Name: "Halt", FileSize: 4,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SetStatus(1, d.ID, StatusMoving); err != nil {
		t.Fatalf("SetStatus moving: %v", err)
	}
	s.RegisterDownload("Halt")

	job := w.tracker.Start("Halt", "download-move", 1, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.runCompletionMove(*d, "Halt", []string{"Halt/missing.mkv"}, false, 4, job)
	}()
	time.Sleep(50 * time.Millisecond) // let attempt 1 fail and park in the backoff
	close(w.stop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runCompletionMove did not return after w.stop was closed")
	}

	got, err := store.Get(1, d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusMoving {
		t.Fatalf("status = %q (error %q), want moving so boot rescue re-dispatches it", got.Status, got.Error)
	}
	if !s.IsDownloadProtected("Halt") {
		t.Fatal("shutdown-interrupted move must keep the eviction protection (data not relocated yet)")
	}
	if st := job.Snapshot().Status; st == transfer.StatusFailed {
		t.Fatalf("job status = %q, shutdown is not a move failure", st)
	}
}

// The retry loop reports shutdown with the sentinel, not with the last move
// error, so callers can tell "gave up" from "interrupted".
func TestAttemptCompletionMove_ShutdownReturnsSentinel(t *testing.T) {
	store := dlwNewStore(t)
	w := NewWorker(WorkerConfig{
		Store:       store,
		Streamer:    streamer.NewForTesting(),
		DataDir:     t.TempDir(),
		DownloadDir: t.TempDir(),
		Tracker:     transfer.New(),
	})
	w.moveBackoff = time.Hour
	close(w.stop)

	d := Download{ID: 1, UserID: 1, InfoHash: "halt", Name: "Halt"}
	job := w.tracker.Start("Halt", "download-move", 1, 4)
	dst, err := w.attemptCompletionMove(d, "Halt", []string{"Halt/missing.mkv"}, false, job)
	if err != errMoveShutdown {
		t.Fatalf("err = %v, want errMoveShutdown", err)
	}
	if dst != "" {
		t.Fatalf("dst = %q, want empty", dst)
	}
}
