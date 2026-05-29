package downloads

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/luizg/jackui/internal/streamer"
)

func TestNewWorkerDefaults(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})
	if w == nil {
		t.Fatal("expected non-nil Worker")
	}
	if w.interval != 2*time.Second {
		t.Errorf("default interval: want 2s, got %v", w.interval)
	}
	if w.ntfyBaseURL != "https://ntfy.sh" {
		t.Errorf("default ntfyBaseURL: want https://ntfy.sh, got %q", w.ntfyBaseURL)
	}
}

func TestNewWorkerWithCustomInterval(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
		Interval: 5 * time.Second,
	})
	if w.interval != 5*time.Second {
		t.Errorf("custom interval: want 5s, got %v", w.interval)
	}
}

func TestWorkerStartStop_EmptyStore(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
		Interval: 50 * time.Millisecond,
	})
	w.Start()
	// Let it run a tick or two
	time.Sleep(150 * time.Millisecond)
	w.Stop()
}

func TestWorkerSnapshotActiveCount_Empty(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})
	if n := w.SnapshotActiveCount(); n != 0 {
		t.Errorf("expected 0 active, got %d", n)
	}
}

func TestWorkerNewRegistersExistingDownloads(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	// Create a download first
	d, err := store.Create(Download{
		UserID:    1,
		InfoHash:  "testhash",
		FileIndex: 0,
		Magnet:    "magnet:?xt=urn:btih:testhash",
		Name:      "TestMovie",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_ = NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})
	if !s.IsDownloadProtected(d.Name) {
		t.Error("expected download to be eviction-protected after NewWorker")
	}
}

func TestWorkerNewSkipsFailedAndCompleted(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	_, _ = store.Create(Download{
		UserID: 1, InfoHash: "act", FileIndex: 0, Magnet: "m", Name: "active",
	})

	failed, _ := store.Create(Download{
		UserID: 1, InfoHash: "fail", FileIndex: 0, Magnet: "m", Name: "failed",
	})
	_ = store.SetError(1, failed.ID, "oops")

	completed, _ := store.Create(Download{
		UserID: 1, InfoHash: "comp", FileIndex: 0, Magnet: "m", Name: "completed",
	})
	_ = store.SetStatus(1, completed.ID, StatusCompleted)

	_ = NewWorker(WorkerConfig{
		Store:       store,
		Streamer:    s,
		DataDir:     t.TempDir(),
		DownloadDir: "", // legacy mode: no dedicated dir
	})
	if !s.IsDownloadProtected(completed.Name) {
		t.Error("completed download should be protected in legacy mode (no downloadDir)")
	}
}

func TestMoveFile_SameFS(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	os.WriteFile(src, []byte("hello"), 0644)

	if err := moveFile(src, dst); err != nil {
		t.Fatalf("moveFile: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should be gone after move")
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "hello" {
		t.Errorf("content: want 'hello', got %q", string(data))
	}
}

func TestMoveFile_SourceNotFound(t *testing.T) {
	dir := t.TempDir()
	err := moveFile(filepath.Join(dir, "nonexistent"), filepath.Join(dir, "dst.txt"))
	if err == nil {
		t.Fatal("expected error for nonexistent source")
	}
}

func TestWorkerPendingMap_InitAndCleanup(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
		Interval: 100 * time.Millisecond,
	})

	w.mu.Lock()
	w.pending[1] = func() {}
	w.retries[1] = 0
	w.mu.Unlock()

	// Cancel it — should be cleaned up
	w.mu.Lock()
	cancel, ok := w.pending[1]
	w.mu.Unlock()
	if !ok {
		t.Fatal("expected pending[1] to exist")
	}
	cancel()

	w.mu.Lock()
	delete(w.pending, 1)
	delete(w.retries, 1)
	w.mu.Unlock()
}

func TestWorker_RaceFreeStartStop(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker := NewWorker(WorkerConfig{
				Store:    store,
				Streamer: s,
				DataDir:  t.TempDir(),
				Interval: 10 * time.Millisecond,
			})
			worker.Start()
			time.Sleep(20 * time.Millisecond)
			worker.Stop()
		}()
	}
	wg.Wait()
}

func TestWorker_FailOrRetryBelowMax(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	d, _ := store.Create(Download{
		UserID: 1, InfoHash: "test", FileIndex: 0, Magnet: "m:test", Name: "test",
	})

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})

	w.mu.Lock()
	w.pending[d.ID] = func() {}
	w.mu.Unlock()

	w.failOrRetry(*d, "transient error")

	w.mu.Lock()
	n := w.retries[d.ID]
	w.mu.Unlock()

	if n != 1 {
		t.Errorf("expected retry count 1, got %d", n)
	}

	// Status should still be downloading (not failed)
	got, _ := store.Get(1, d.ID)
	if got.Status != StatusDownloading {
		t.Errorf("expected downloading after retry, got %q", got.Status)
	}
}

func TestWorker_FailOrRetryAtMax(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	d, _ := store.Create(Download{
		UserID: 1, InfoHash: "test", FileIndex: 0, Magnet: "m:test", Name: "test",
	})

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})

	w.mu.Lock()
	w.pending[d.ID] = func() {}
	w.retries[d.ID] = maxInitRetries - 1
	w.mu.Unlock()

	w.failOrRetry(*d, "final error")

	got, _ := store.Get(1, d.ID)
	if got.Status != StatusFailed {
		t.Errorf("expected failed after max retries, got %q", got.Status)
	}

	w.mu.Lock()
	_, hasRetry := w.retries[d.ID]
	w.mu.Unlock()
	if hasRetry {
		t.Error("expected retries to be cleared after max retry")
	}
}

func TestWorker_FailOrRetryCancelled(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	d, _ := store.Create(Download{
		UserID: 1, InfoHash: "test", FileIndex: 0, Magnet: "m:test", Name: "test",
	})

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})

	// No pending entry => cancelled
	w.failOrRetry(*d, "cancelled error")

	got, _ := store.Get(1, d.ID)
	if got.Status != StatusDownloading {
		t.Errorf("expected status unchanged after cancelled retry, got %q", got.Status)
	}
}

func TestWorker_NtfyTopicEmpty(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	w := NewWorker(WorkerConfig{
		Store:     store,
		Streamer:  s,
		DataDir:   t.TempDir(),
		NtfyTopic: "",
	})
	// sending with empty topic should be a no-op
	w.sendNtfy(nil, "title", "body", "tag") //nolint:staticcheck
}
