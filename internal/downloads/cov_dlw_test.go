package downloads

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// dlwNewStore mirrors the package's newTestStore helper but with a dlw-prefixed
// name so this file is self-contained against renames in the shared helper.
func dlwNewStore(t *testing.T) *Store {
	t.Helper()
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	s, err := New(pool)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func dlwNewWorker(t *testing.T, store *Store, dataDir, downloadDir string) *Worker {
	t.Helper()
	return NewWorker(WorkerConfig{
		Store:       store,
		Streamer:    streamer.NewForTesting(),
		DataDir:     dataDir,
		DownloadDir: downloadDir,
	})
}

// ─── FindByPathPrefix (was 0% covered) ─────────────────────────────────────

func Test_dlw_FindByPathPrefix_ExactAndPrefix(t *testing.T) {
	s := dlwNewStore(t)
	// Three completed downloads with absolute file paths; one outside the dir.
	mk := func(hash, path string) {
		d, err := s.Create(Download{UserID: 1, InfoHash: hash, FileIndex: 0, Magnet: "magnet:?xt=urn:btih:" + hash, Name: hash})
		if err != nil {
			t.Fatalf("Create %s: %v", hash, err)
		}
		if err := s.SetFilePath(1, d.ID, path); err != nil {
			t.Fatalf("SetFilePath: %v", err)
		}
	}
	mk("h1", "/data/movies/a.mkv")
	mk("h2", "/data/movies/sub/b.mkv")
	mk("h3", "/data/other/c.mkv")

	// Prefix match: the directory should match both files beneath it.
	under, err := s.FindByPathPrefix("/data/movies")
	if err != nil {
		t.Fatalf("FindByPathPrefix dir: %v", err)
	}
	if len(under) != 2 {
		t.Fatalf("expected 2 downloads under /data/movies, got %d (%+v)", len(under), under)
	}

	// Exact match: the file path itself.
	exact, err := s.FindByPathPrefix("/data/other/c.mkv")
	if err != nil {
		t.Fatalf("FindByPathPrefix exact: %v", err)
	}
	if len(exact) != 1 || exact[0].FilePath != "/data/other/c.mkv" {
		t.Fatalf("expected exact match for c.mkv, got %+v", exact)
	}

	// A trailing-slash directory normalizes the same way.
	trailing, err := s.FindByPathPrefix("/data/movies/")
	if err != nil {
		t.Fatalf("FindByPathPrefix trailing: %v", err)
	}
	if len(trailing) != 2 {
		t.Fatalf("expected 2 under /data/movies/, got %d", len(trailing))
	}
}

func Test_dlw_FindByPathPrefix_NoMatch(t *testing.T) {
	s := dlwNewStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:h", Name: "n"})
	_ = s.SetFilePath(1, d.ID, "/srv/files/x.mkv")

	out, err := s.FindByPathPrefix("/nothing/here")
	if err != nil {
		t.Fatalf("FindByPathPrefix: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no matches, got %d", len(out))
	}
}

func Test_dlw_FindByPathPrefix_EmptyAndNil(t *testing.T) {
	s := dlwNewStore(t)
	// Empty path → nil, no error.
	out, err := s.FindByPathPrefix("")
	if err != nil || out != nil {
		t.Fatalf("empty path: out=%v err=%v", out, err)
	}
	// Nil receiver → nil, no error.
	var nilS *Store
	out, err = nilS.FindByPathPrefix("/x")
	if err != nil || out != nil {
		t.Fatalf("nil store: out=%v err=%v", out, err)
	}
}

func Test_dlw_FindByPathPrefix_SkipsEmptyFilePath(t *testing.T) {
	s := dlwNewStore(t)
	// A download with no file_path must never match a prefix query.
	_, _ = s.Create(Download{UserID: 1, InfoHash: "nofp", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:nofp", Name: "n"})
	out, err := s.FindByPathPrefix("/")
	if err != nil {
		t.Fatalf("FindByPathPrefix: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty-file_path rows excluded, got %d", len(out))
	}
}

// ─── moveCompletedFile success path (was low coverage) ─────────────────────

// dlwFakeFile lets us exercise moveCompletedFile without a live torrent.File —
// but moveCompletedFile reads td.file.Path(), which requires a real *torrent.File.
// Instead we test the move plumbing end-to-end via the exported moveFile and the
// SetFilePath bookkeeping that moveCompletedFile relies on.
func Test_dlw_MoveFile_OverwritesExistingDest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")
	if err := os.WriteFile(src, []byte("new-content"), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.WriteFile(dst, []byte("old-content-longer"), 0644); err != nil {
		t.Fatalf("write dst: %v", err)
	}
	if err := moveFile(src, dst); err != nil {
		t.Fatalf("moveFile: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "new-content" {
		t.Fatalf("dst content = %q, want overwrite to 'new-content'", string(got))
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("src should be gone, stat err=%v", err)
	}
}

func Test_dlw_MoveFile_DestDirMissing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	// Destination parent directory does not exist → both rename and OpenFile fail.
	dst := filepath.Join(dir, "missing-subdir", "dst.bin")
	if err := moveFile(src, dst); err == nil {
		t.Fatal("expected error when dest parent dir is missing")
	}
	// Source must be left intact when the move fails.
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("src should remain after failed move: %v", err)
	}
}

// ─── initDownload error path via startInit (load-torrent failure + retry) ──

func Test_dlw_StartInit_InvalidMagnetRetries(t *testing.T) {
	store := dlwNewStore(t)
	// "m:hash" is not a valid magnet/URL → EnsureActive fails fast (no network),
	// driving initDownload → failOrRetry on the transient-failure branch.
	d, err := store.Create(Download{UserID: 1, InfoHash: "badhash", FileIndex: 0, Magnet: "m:hash", Name: "bad"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// init runs only on downloading rows — promote as the scheduler would.
	_, _ = store.PromoteToDownloading(d.ID)
	d, _ = store.Get(1, d.ID)
	w := dlwNewWorker(t, store, t.TempDir(), "")

	w.startInit(*d)
	// initDownload runs in a goroutine tracked by doneWG; wait for it to finish.
	w.doneWG.Wait()

	// First failure is transient: status stays downloading, retry count incremented.
	got, _ := store.Get(1, d.ID)
	if got.Status != StatusDownloading {
		t.Fatalf("expected downloading after first transient failure, got %q", got.Status)
	}
	w.mu.Lock()
	n := w.retries[d.ID]
	_, stillPending := w.pending[d.ID]
	w.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected retry count 1, got %d", n)
	}
	if stillPending {
		t.Fatal("expected pending entry cleared after initDownload returns")
	}
}

func Test_dlw_StartInit_InvalidMagnetReachesFailed(t *testing.T) {
	store := dlwNewStore(t)
	d, err := store.Create(Download{UserID: 1, InfoHash: "deadhash", FileIndex: 0, Magnet: "m:dead", Name: "dead"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	w := dlwNewWorker(t, store, t.TempDir(), "")

	// Run init enough times to exceed maxInitRetries → row flips to failed.
	for i := 0; i < maxInitRetries; i++ {
		w.startInit(*d)
		w.doneWG.Wait()
	}

	got, _ := store.Get(1, d.ID)
	if got.Status != StatusFailed {
		t.Fatalf("expected failed after %d retries, got %q (err=%q)", maxInitRetries, got.Status, got.Error)
	}
	if got.Error == "" {
		t.Fatal("expected error message persisted on failed download")
	}
}

// ─── sampleProgress regression + normal update branches ────────────────────

func Test_dlw_SampleProgress_NilFileNoop(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")
	// Explicit nil-file trackedDL: must not panic and must not write progress.
	d, _ := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:h", Name: "n"})
	td := &trackedDL{id: d.ID, file: nil}
	w.sampleProgress(*d, td)
	got, _ := store.Get(1, d.ID)
	if got.BytesDownloaded != 0 {
		t.Fatalf("expected no progress write, got %d", got.BytesDownloaded)
	}
}

// ─── ntfy backoff / request-error branches ─────────────────────────────────

func Test_dlw_SendNtfy_BadBaseURLRequestError(t *testing.T) {
	store := dlwNewStore(t)
	// A control character in the base URL makes http.NewRequestWithContext fail,
	// hitting the early-return error branch in sendNtfy.
	w := NewWorker(WorkerConfig{
		Store:       store,
		Streamer:    streamer.NewForTesting(),
		DataDir:     t.TempDir(),
		NtfyBaseURL: "http://invalid\x7f-host",
		NtfyTopic:   "topic",
	})
	// Should return promptly without panicking (request build fails).
	done := make(chan struct{})
	go func() {
		w.sendNtfy(context.Background(), "title", "body", "tag")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sendNtfy did not return on request build error")
	}
}

func Test_dlw_SendNtfy_CancelledContext(t *testing.T) {
	store := dlwNewStore(t)
	// Point at a closed/refused port so the first Do() fails, then the cancelled
	// context short-circuits the backoff sleep.
	w := NewWorker(WorkerConfig{
		Store:       store,
		Streamer:    streamer.NewForTesting(),
		DataDir:     t.TempDir(),
		NtfyBaseURL: "http://127.0.0.1:1",
		NtfyTopic:   "topic",
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	done := make(chan struct{})
	go func() {
		w.sendNtfy(ctx, "title", "body", "tag")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("sendNtfy did not honor cancelled context")
	}
}

// ─── Stop cancels in-flight pending inits ──────────────────────────────────

func Test_dlw_Stop_CancelsPending(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")

	// Seed a pending cancel func, then call Stop WITHOUT Start() — the run loop's
	// tick would also fire the cancel for an id absent from the active set, so we
	// avoid that race and exercise Stop's own in-flight cancel loop directly.
	var calls int32
	w.mu.Lock()
	w.pending[42] = func() { atomic.AddInt32(&calls, 1) }
	w.mu.Unlock()

	// Stop does close(w.stop) then doneWG.Wait(); nothing added to the WaitGroup
	// here, so Wait() returns immediately after the cancel loop runs.
	w.Stop()

	if atomic.LoadInt32(&calls) == 0 {
		t.Fatal("expected Stop to invoke pending cancel func")
	}
}

// ─── checkCompletion: zero-length file is not treated as complete ──────────

func Test_dlw_CheckCompletion_NilFileNoop(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")
	d, _ := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:h", Name: "n"})
	td := &trackedDL{id: d.ID, file: nil}
	w.checkCompletion(*d, td)
	got, _ := store.Get(1, d.ID)
	if got.Status == StatusCompleted {
		t.Fatal("nil-file checkCompletion should not mark completed")
	}
}

// ─── Delete is idempotent for a missing row ────────────────────────────────

func Test_dlw_Delete_MissingRowIsIdempotent(t *testing.T) {
	s := dlwNewStore(t)
	if err := s.Delete(1, 999999); err != nil {
		t.Fatalf("Delete of a missing row must be idempotent, got: %v", err)
	}
}

// ─── scanGeneric: progress clamps to 1 when bytes exceed size ──────────────

func Test_dlw_ScanGeneric_ProgressClampedToOne(t *testing.T) {
	s := dlwNewStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:h", Name: "n", FileSize: 100})
	// Persist more bytes than the file size (can happen if size metadata lags).
	if err := s.UpdateProgress(1, d.ID, 250); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.Progress != 1 {
		t.Fatalf("expected progress clamped to 1.0, got %v", got.Progress)
	}
}

// ─── resolveFileIndex coverage test ────────────────────────────────────────

func Test_dlw_ResolveFileIndex(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")

	// Caso 1: Index válido (dentro do slice de files)
	f1 := &torrent.File{}
	files := []*torrent.File{f1}

	d1 := Download{UserID: 1, ID: 101, FileIndex: 0, InfoHash: "h1", Magnet: "magnet:?xt=urn:btih:h1"}
	idx, ok := w.resolveFileIndex(&d1, files)
	if !ok || idx != 0 {
		t.Fatalf("esperava index 0 e ok=true, obteve %d and %v", idx, ok)
	}

	// Caso 2: Index fora dos limites, mas pickBestFile retorna -1 (slice vazio)
	d2 := Download{UserID: 1, ID: 102, FileIndex: -1, InfoHash: "h2", Magnet: "magnet:?xt=urn:btih:h2"}
	createdD2, errCreate := store.Create(d2)
	if errCreate != nil {
		t.Fatalf("Create: %v", errCreate)
	}
	idx, ok = w.resolveFileIndex(createdD2, []*torrent.File{})
	if ok || idx != -1 {
		t.Fatalf("esperava index -1 e ok=false para slice vazio, obteve %d and %v", idx, ok)
	}
	got, _ := store.Get(1, createdD2.ID)
	if got == nil {
		t.Fatal("expected download to exist in store")
	}
	if got.Status != StatusFailed {
		t.Fatalf("esperava status failed, obteve %s", got.Status)
	}
}
