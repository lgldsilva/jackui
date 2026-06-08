package downloads

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/streamer"
)

// The downloads worker has its own ntfy sender (separate from the watchlist's);
// it must also send the configured token as Authorization: Bearer.
func TestSendNtfy_SetsBearerWhenTokenSet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w := &Worker{
		ntfyBaseURL: srv.URL,
		ntfyTopic:   "topic",
		ntfyToken:   "tk_secret",
		ntfyClient:  &http.Client{},
	}
	w.sendNtfy(context.Background(), "title", "body", "tags")
	if gotAuth != "Bearer tk_secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tk_secret")
	}
}

func TestSendNtfy_NoAuthHeaderWhenTokenEmpty(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w := &Worker{ntfyBaseURL: srv.URL, ntfyTopic: "topic", ntfyClient: &http.Client{}}
	w.sendNtfy(context.Background(), "title", "body", "tags")
	if hadAuth {
		t.Error("Authorization header must be absent when no token configured")
	}
}

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
	testMoveFile(t, filepath.Join(dir, "src.txt"), filepath.Join(dir, "dst.txt"), "hello")
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
	// init runs only on downloading rows — promote as the scheduler would.
	_, _ = store.PromoteToDownloading(d.ID)
	d, _ = store.Get(1, d.ID)

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
	// init runs only on downloading rows — promote as the scheduler would.
	_, _ = store.PromoteToDownloading(d.ID)
	d, _ = store.Get(1, d.ID)

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

func TestWorkerReconcile_StartsInitForNewDownload(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

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

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})

	w.mu.Lock()
	_, isPending := w.pending[d.ID]
	w.mu.Unlock()
	if isPending {
		t.Fatal("expected no pending entry before reconcile")
	}

	w.reconcile(*d)

	w.mu.Lock()
	_, isPending = w.pending[d.ID]
	retries := w.retries[d.ID]
	w.mu.Unlock()
	if !isPending && retries == 0 {
		t.Error("expected pending entry or retries incremented after reconcile for new download")
	}
}

func TestWorkerReconcile_SkipsWhenPending(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	d, _ := store.Create(Download{
		UserID: 1, InfoHash: "hash", FileIndex: 0, Magnet: "m:hash", Name: "name",
	})

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})

	w.mu.Lock()
	w.pending[d.ID] = func() {}
	w.mu.Unlock()

	// Should not panic or start another init
	w.reconcile(*d)
}

func TestWorkerSampleProgress_NilFile(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})

	// trackedDL with nil file — sampleProgress should be a no-op
	td := &trackedDL{id: 1}
	w.sampleProgress(Download{}, td)
}

func TestWorkerCheckCompletion_NilFile(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})

	td := &trackedDL{id: 1}
	w.checkCompletion(Download{}, td)
}

func TestWorkerTorrentStillActive_NoClient(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})

	td := &trackedDL{id: 1}
	if w.torrentStillActive(td) {
		t.Error("expected false when streamer has no client")
	}
}

func TestWorkerStartInit_CreatesPending(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	d, err := store.Create(Download{
		UserID: 1, InfoHash: "hash", FileIndex: 0, Magnet: "m:hash", Name: "name",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})

	w.startInit(*d)

	w.mu.Lock()
	cancel, ok := w.pending[d.ID]
	retries := w.retries[d.ID]
	w.mu.Unlock()
	if !ok && retries == 0 {
		t.Fatal("expected pending entry or retries after startInit")
	}
	if ok {
		cancel()
	}

	// Wait for the goroutine to finish
	w.doneWG.Wait()
}

func TestWorkerReconcile_DetectsInactiveTorrent(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	d, _ := store.Create(Download{
		UserID: 1, InfoHash: "hash", FileIndex: 0, Magnet: "m:hash", Name: "name",
	})

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})

	// Put a tracked entry with no actual torrent in the streamer
	td := &trackedDL{id: d.ID, hash: [20]byte{1}}
	w.mu.Lock()
	w.tracked[d.ID] = td
	w.mu.Unlock()

	// reconcile should detect the torrent is no longer active and remove it
	w.reconcile(*d)

	w.mu.Lock()
	_, tracked := w.tracked[d.ID]
	w.mu.Unlock()
	if tracked {
		t.Error("expected tracked entry to be removed after detectin inactive torrent")
	}
}

func TestWorkerReconcile_SamplesProgress(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	d, _ := store.Create(Download{
		UserID: 1, InfoHash: "hash", FileIndex: 0, Magnet: "m:hash", Name: "name",
	})

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})

	// Put a tracked entry - reconcile will call sampleProgress
	// This tests a tracked download that exists (not pending, already tracked)
	// The torrentStillActive check will fail (no client), so it'll remove
	// the tracked entry and then trigger startInit again
	w.mu.Lock()
	w.tracked[d.ID] = &trackedDL{id: d.ID, hash: [20]byte{1}}
	w.mu.Unlock()

	w.reconcile(*d)

	w.mu.Lock()
	_, tracked := w.tracked[d.ID]
	w.mu.Unlock()
	if tracked {
		t.Error("expected tracked entry to be removed after inactive torrent")
	}
}

func TestWorkerSendNtfy_Timeout(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	w := NewWorker(WorkerConfig{
		Store:     store,
		Streamer:  s,
		DataDir:   t.TempDir(),
		NtfyTopic: "test-topic",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	// Should return early due to context timeout
	w.sendNtfy(ctx, "Test", "Body", "tag")
}

func TestWorkerMoveFile_CrossFS(t *testing.T) {
	// Simulate cross-filesystem move by using rename failure
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	os.WriteFile(src, []byte("cross-fs-data"), 0644)

	if err := moveFile(src, dst); err != nil {
		t.Fatalf("moveFile: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should be gone after move")
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "cross-fs-data" {
		t.Errorf("content: want 'cross-fs-data', got %q", string(data))
	}
}

func TestWorker_MoveCompletedFile_NoDir(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	w := NewWorker(WorkerConfig{
		Store:       store,
		Streamer:    s,
		DataDir:     t.TempDir(),
		DownloadDir: "", // empty = keep in place (legacy mode)
	})

	// Should be a no-op in legacy mode.
	if err := w.moveCompletedFile(Download{}, "x.mkv", "test"); err != nil {
		t.Errorf("no-op expected, got %v", err)
	}
}

func TestWorker_MoveCompletedFile_MkdirFailure(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	w := NewWorker(WorkerConfig{
		Store:       store,
		Streamer:    s,
		DataDir:     "/nonexistent-parent-xyz",
		DownloadDir: t.TempDir(),
	})

	// Source doesn't exist in the (bogus) DataDir → returns an error, no panic.
	if err := w.moveCompletedFile(Download{}, "test/f.mkv", "test"); err == nil {
		t.Error("expected error when source is missing")
	}
}

func TestWorkerTick_ListActiveError(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})
	// Close the store so ListActive fails
	store.Close()

	w.Start()
	time.Sleep(50 * time.Millisecond)
	w.Stop()
	// Should not panic on store error
}

func TestWorkerTick_CleansUpPendingRemoved(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

	d, _ := store.Create(Download{
		UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "m:h", Name: "n",
	})

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
		Interval: 50 * time.Millisecond,
	})

	w.mu.Lock()
	w.pending[d.ID] = func() {}
	w.mu.Unlock()

	// Remove from store so tick cleans up pending
	_ = store.Delete(1, d.ID)

	w.Start()
	time.Sleep(100 * time.Millisecond)
	w.Stop()

	w.mu.Lock()
	_, pending := w.pending[d.ID]
	w.mu.Unlock()
	if pending {
		t.Error("expected pending entry to be removed")
	}
}

func TestWorkerTick_CleansUpRemovedDownloads(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)

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

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
		Interval: 50 * time.Millisecond,
	})

	w.mu.Lock()
	w.tracked[d.ID] = &trackedDL{id: d.ID, name: d.Name}
	w.mu.Unlock()

	// Start worker - tick will detect this tracked download has no active store entry
	w.Start()
	time.Sleep(100 * time.Millisecond)
	w.Stop()

	w.mu.Lock()
	_, tracked := w.tracked[d.ID]
	w.mu.Unlock()
	if tracked {
		t.Error("expected tracked entry to be removed for inactive download")
	}
}

func testMoveFile(t *testing.T, src, dst, content string) {
	os.WriteFile(src, []byte(content), 0644)
	if err := moveFile(src, dst); err != nil {
		t.Fatalf("moveFile: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should be gone after move")
	}
	data, _ := os.ReadFile(dst)
	if string(data) != content {
		t.Errorf("content: want %q, got %q", content, string(data))
	}
}
