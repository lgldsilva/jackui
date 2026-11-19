package downloads

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync"
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
	logs := captureLog(t)
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

	d, err := store.Create(context.Background(), Download{
		UserID: 1, InfoHash: "halt", FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:halt", Name: "Halt", FileSize: 4,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SetStatus(context.Background(), 1, d.ID, StatusMoving); err != nil {
		t.Fatalf("SetStatus moving: %v", err)
	}
	s.RegisterDownload("Halt")

	job := w.tracker.Start("Halt", "download-move", 1, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.runCompletionMove(*d, "Halt", []string{"Halt/missing.mkv"}, false, 4, job)
	}()
	// Attempt 1 logs its failure right before parking in the backoff select;
	// closing stop only after that line makes the test cover the "interrupted
	// DURING backoff" path rather than racing a fixed sleep against it.
	waitForLog(t, logs, "attempt 1/")
	close(w.stop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runCompletionMove did not return after w.stop was closed")
	}

	got, err := store.Get(context.Background(), 1, d.ID)
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

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func captureLog(t *testing.T) *lockedBuf {
	t.Helper()
	buf := &lockedBuf{}
	prev := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return buf
}

func waitForLog(t *testing.T, buf *lockedBuf, substr string) {
	t.Helper()
	waitFor(t, "log line "+substr, func() bool { return strings.Contains(buf.String(), substr) })
}
