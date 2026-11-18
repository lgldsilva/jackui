package downloads

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/metrics/events"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func initFailures(outcome string) float64 {
	return testutil.ToFloat64(events.DownloadInitFailures.WithLabelValues(outcome))
}

func completions(kind string) float64 {
	return testutil.ToFloat64(events.DownloadCompletions.WithLabelValues(kind))
}

func moveFailures(kind string) float64 {
	return testutil.ToFloat64(events.DownloadMoveFailures.WithLabelValues(kind))
}

// Init failures (dead magnet / GotInfo timeout) are counted per attempt as
// retry, and once more as failed when the retries run out.
func TestDownloadMetrics_InitFailures(t *testing.T) {
	store := dlwNewStore(t)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "initm", FileIndex: 0, Magnet: "m:initm", Name: "x"})
	_, _ = store.PromoteToDownloading(d.ID)
	d, _ = store.Get(1, d.ID)
	w := NewWorker(WorkerConfig{Store: store, Streamer: streamer.NewForTesting(), DataDir: t.TempDir()})

	retry, failed := initFailures("retry"), initFailures("failed")
	w.mu.Lock()
	w.pending[d.ID] = func() {}
	w.mu.Unlock()
	w.failOrRetry(context.Background(), *d, "transient")
	if got := initFailures("retry") - retry; got != 1 {
		t.Errorf("retry delta = %v, want 1", got)
	}

	w.mu.Lock()
	w.retries[d.ID] = maxInitRetries - 1
	w.mu.Unlock()
	w.failOrRetry(context.Background(), *d, "final")
	if got := initFailures("failed") - failed; got != 1 {
		t.Errorf("failed delta = %v, want 1", got)
	}
}

// Stall detection counts every demotion and, at MaxStalls, the pause.
func TestDownloadMetrics_StallsAndPause(t *testing.T) {
	store := dlwNewStore(t)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "stallm", FileIndex: 0, Magnet: "m", Name: "S"})
	_, _ = store.PromoteToDownloading(d.ID)
	w := newQueueWorker(t, store, QueueSettings{MaxActive: 3, StallThresholdMin: 30, MaxStalls: 2})
	inject := func() {
		w.mu.Lock()
		w.tracked[d.ID] = &trackedDL{id: d.ID, userID: 1, name: "S", lastProgressAt: time.Now().Add(-time.Hour)}
		w.mu.Unlock()
	}
	stalls := testutil.ToFloat64(events.DownloadStalls)
	pauses := testutil.ToFloat64(events.DownloadStallPauses)

	inject()
	w.detectStalls(w.queueSettings())
	_, _ = store.PromoteToDownloading(d.ID)
	inject()
	w.detectStalls(w.queueSettings())

	if got := testutil.ToFloat64(events.DownloadStalls) - stalls; got != 2 {
		t.Errorf("stalls delta = %v, want 2", got)
	}
	if got := testutil.ToFloat64(events.DownloadStallPauses) - pauses; got != 1 {
		t.Errorf("stall pauses delta = %v, want 1", got)
	}
}

func metricsMoveWorker(t *testing.T, store *Store, dataDir, downloadDir string) *Worker {
	t.Helper()
	w := NewWorker(WorkerConfig{
		Store: store, Streamer: streamer.NewForTesting(),
		DataDir: dataDir, DownloadDir: downloadDir, Tracker: transfer.New(),
	})
	w.moveBackoff = time.Millisecond
	return w
}

// A single-row completion move: success → completions{single}; a move that
// gives up → move_failures{single}.
func TestDownloadMetrics_SingleCompletionMove(t *testing.T) {
	store := dlwNewStore(t)
	dataDir := t.TempDir()
	w := metricsMoveWorker(t, store, dataDir, t.TempDir())

	if err := os.MkdirAll(filepath.Join(dataDir, "Ok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "Ok", "f.mkv"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, _ := store.Create(Download{UserID: 1, InfoHash: "okm", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:okm", Name: "Ok", FileSize: 4})
	before := completions(events.DownloadKindSingle)
	w.runCompletionMove(*ok, "Ok", []string{"Ok/f.mkv"}, false, 4, w.tracker.Start("Ok", "download-move", 1, 4))
	if got := completions(events.DownloadKindSingle) - before; got != 1 {
		t.Errorf("completions{single} delta = %v, want 1", got)
	}

	bad, _ := store.Create(Download{UserID: 1, InfoHash: "badm", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:badm", Name: "Bad", FileSize: 4})
	beforeFail := moveFailures(events.DownloadKindSingle)
	w.runCompletionMove(*bad, "Bad", []string{"Bad/missing.mkv"}, false, 4, w.tracker.Start("Bad", "download-move", 1, 4))
	if got := moveFailures(events.DownloadKindSingle) - beforeFail; got != 1 {
		t.Errorf("move_failures{single} delta = %v, want 1", got)
	}
}

// A multi-file group: success → completions{group}; a cancelled/failed move →
// move_failures{group}.
func TestDownloadMetrics_GroupCompletionMove(t *testing.T) {
	store := dlwNewStore(t)
	// No destination configured: the group finalizes in place (legacy mode).
	w := metricsMoveWorker(t, store, t.TempDir(), "")
	a, _ := store.Create(Download{UserID: 1, InfoHash: "grpm", FileIndex: 0, Magnet: "m", Name: "G"})
	b, _ := store.Create(Download{UserID: 1, InfoHash: "grpm", FileIndex: 1, Magnet: "m", Name: "G"})
	g := Group{Key: "1|grpm", UserID: 1, Members: []Download{*a, *b}}
	movers := []groupMover{{row: *a, relPath: "G/a.mkv", length: 1}, {row: *b, relPath: "G/b.mkv", length: 1}}

	before := completions(events.DownloadKindGroup)
	w.runGroupCompletionMove(g, "G", movers, 2, w.tracker.Start("G", "download-move", 2, 2))
	if got := completions(events.DownloadKindGroup) - before; got != 1 {
		t.Errorf("completions{group} delta = %v, want 1", got)
	}

	job := w.tracker.Start("G", "download-move", 2, 2)
	if !w.tracker.Cancel(job.ID(), 0, true) {
		t.Fatal("cancel job")
	}
	beforeFail := moveFailures(events.DownloadKindGroup)
	w.runGroupCompletionMove(g, "G", movers, 2, job)
	if got := moveFailures(events.DownloadKindGroup) - beforeFail; got != 1 {
		t.Errorf("move_failures{group} delta = %v, want 1", got)
	}
}
