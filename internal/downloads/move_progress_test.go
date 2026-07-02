package downloads

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// moveFileProgress reports the bytes copied to onBytes — same-filesystem rename
// reports the whole size in one shot (so the bar still lands at 100%).
func TestMoveFileProgress_ReportsBytesOnRename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got int64
	if err := moveFileProgress(context.Background(), src, filepath.Join(dir, "b.bin"), func(n int64) { got += n }); err != nil {
		t.Fatalf("moveFileProgress: %v", err)
	}
	if got != 5 {
		t.Fatalf("reported %d bytes, want 5", got)
	}
}

// The cross-filesystem fallback streams through transfer.ProgressReader, so
// onBytes sees the copy advance; src is removed and dst lands with the content.
func TestMoveFileProgress_CopyFallbackReportsBytes(t *testing.T) {
	orig := renameFn
	renameFn = func(string, string) error { return syscall.EXDEV }
	t.Cleanup(func() { renameFn = orig })

	dir := t.TempDir()
	src := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "b.bin")
	var got int64
	if err := moveFileProgress(context.Background(), src, dst, func(n int64) { got += n }); err != nil {
		t.Fatalf("moveFileProgress (copy): %v", err)
	}
	if got != 100 {
		t.Fatalf("reported %d bytes, want 100", got)
	}
	if fileExists(src) {
		t.Error("src must be removed after a successful copy")
	}
	if !fileExists(dst) {
		t.Error("dst must exist after a successful copy")
	}
}

// A row left in `moving` by a restart is rescued on boot: flipped back to
// `downloading` so the next tick re-dispatches the (idempotent) move.
func TestRescueInterruptedMove_RequeuesMovingOnBoot(t *testing.T) {
	store := dlwNewStore(t)
	d, err := store.Create(Download{
		UserID: 1, InfoHash: "mv", FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:mv", Name: "Pack", FileSize: 4,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SetStatus(1, d.ID, StatusMoving); err != nil {
		t.Fatalf("SetStatus moving: %v", err)
	}
	// NewWorker runs registerExistingDownloads → rescueInterruptedMove.
	NewWorker(WorkerConfig{Store: store, Streamer: streamer.NewForTesting(), DataDir: t.TempDir(), DownloadDir: t.TempDir()})
	got, _ := store.Get(1, d.ID)
	if got.Status != StatusDownloading {
		t.Fatalf("status = %q, want downloading (rescued)", got.Status)
	}
}

// The post-download move reports to the transfer tracker: the job ends `done`
// with every file counted (X/Y) and progress at 100%.
func TestRunCompletionMove_ReportsToTracker(t *testing.T) {
	store := dlwNewStore(t)
	s := streamer.NewForTesting()
	dataDir := t.TempDir()
	downloadDir := t.TempDir()
	tr := transfer.New()
	w := NewWorker(WorkerConfig{Store: store, Streamer: s, DataDir: dataDir, DownloadDir: downloadDir, Tracker: tr})
	w.moveBackoff = time.Millisecond
	d, _ := store.Create(Download{
		UserID: 1, InfoHash: "tk", FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:tk", Name: "Pack3", FileSize: 4,
	})
	if err := os.MkdirAll(filepath.Join(dataDir, "Pack3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "Pack3", "only.bin"), []byte("zzzz"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Whole-torrent rel paths include the torrent's root folder (anacrolix
	// f.Path() convention); moveCompletedTree strips it under destDir.
	job := tr.Start("Pack3", "download-move", 1, 4)
	w.runCompletionMove(*d, "Pack3", []string{"Pack3/only.bin"}, true, 4, job)

	got, _ := store.Get(1, d.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	snap := job.Snapshot()
	if snap.Status != transfer.StatusDone {
		t.Fatalf("job status = %q, want done", snap.Status)
	}
	if snap.FilesDone != 1 {
		t.Fatalf("FilesDone = %d, want 1", snap.FilesDone)
	}
	if snap.Progress != 1.0 {
		t.Fatalf("progress = %v, want 1.0", snap.Progress)
	}
}
