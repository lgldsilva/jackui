package downloads

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestResolveCompletedSrc(t *testing.T) {
	dir := t.TempDir()
	// neither final nor .part → empty
	if got := resolveCompletedSrc(dir, "a/b.mkv"); got != "" {
		t.Errorf("no file → expected empty, got %q", got)
	}
	// only the leftover .part → return it
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(dir, "a", "b.mkv.part")
	if err := os.WriteFile(part, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveCompletedSrc(dir, "a/b.mkv"); got != part {
		t.Errorf("part only → expected %q, got %q", part, got)
	}
	// final present → preferred over .part
	final := filepath.Join(dir, "a", "b.mkv")
	if err := os.WriteFile(final, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveCompletedSrc(dir, "a/b.mkv"); got != final {
		t.Errorf("final → expected %q, got %q", final, got)
	}
}

func TestMoveDownloadedFile(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "T"), 0o755); err != nil {
		t.Fatal(err)
	}
	// anacrolix left a complete ".part" (not renamed) — move must still work and
	// land the file at the final name (no .part) in the destination.
	if err := os.WriteFile(filepath.Join(src, "T", "f.mkv.part"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := moveDownloadedFile(src, filepath.Join(dst, "T"), "T/f.mkv", nil)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	want := filepath.Join(dst, "T", "f.mkv")
	if got != want {
		t.Errorf("dst = %q, want %q", got, want)
	}
	if !fileExists(want) {
		t.Error("moved file should exist at dst without the .part suffix")
	}
	// missing source → error
	if _, err := moveDownloadedFile(src, dst, "nope/x.mkv", nil); err == nil {
		t.Error("expected error for missing source")
	}
}

func TestCompletedDestDir(t *testing.T) {
	if got := completedDestDir("/dl", "alice", "Movie"); got != "/dl/alice/Movie" {
		t.Errorf("with user: got %q", got)
	}
	if got := completedDestDir("/dl", "", "Movie"); got != "/dl/Movie" {
		t.Errorf("no user: got %q", got)
	}
}

func TestDirHasFiles(t *testing.T) {
	dir := t.TempDir()
	if dirHasFiles(filepath.Join(dir, "nope")) {
		t.Error("missing dir → false")
	}
	empty := filepath.Join(dir, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if dirHasFiles(empty) {
		t.Error("empty dir → false")
	}
	if err := os.WriteFile(filepath.Join(empty, "x"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !dirHasFiles(empty) {
		t.Error("dir with a file → true")
	}
}

// An orphan (completed, but file_path vanished while the source still sits in the
// cache) must be re-queued on boot so the worker can re-move it without re-downloading.
func TestWorkerReconcilesOrphanOnBoot(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)
	dataDir := t.TempDir()
	downloadDir := t.TempDir()

	d, err := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "m", Name: "MyTorrent"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_ = store.SetStatus(1, d.ID, StatusCompleted)
	// file_path points at a destination file that does NOT exist (move was interrupted)
	_ = store.SetFilePath(1, d.ID, filepath.Join(downloadDir, "MyTorrent", "file.mkv"))
	// but the source (a complete .part) is still in the cache
	srcDir := filepath.Join(dataDir, "MyTorrent")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "file.mkv.part"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	_ = NewWorker(WorkerConfig{Store: store, Streamer: s, DataDir: dataDir, DownloadDir: downloadDir})

	got, _ := store.Get(1, d.ID)
	if got.Status != StatusQueued {
		t.Errorf("orphan should be re-queued on boot, got status %q", got.Status)
	}
}

// A completed download whose file is present must stay completed (not re-queued).
func TestWorkerKeepsCompletedWhenFilePresent(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)
	downloadDir := t.TempDir()

	d, _ := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "m", Name: "Done"})
	_ = store.SetStatus(1, d.ID, StatusCompleted)
	dst := filepath.Join(downloadDir, "file.mkv")
	if err := os.WriteFile(dst, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = store.SetFilePath(1, d.ID, dst)

	_ = NewWorker(WorkerConfig{Store: store, Streamer: s, DataDir: t.TempDir(), DownloadDir: downloadDir})

	got, _ := store.Get(1, d.ID)
	if got.Status != StatusCompleted {
		t.Errorf("present file should stay completed, got %q", got.Status)
	}
}

// Guard against mass re-download: if file_path is missing AND there's no source
// in the cache (e.g. downloadDir briefly unmounted), do NOT re-queue.
func TestWorkerKeepsCompletedWhenNoCacheSource(t *testing.T) {
	s := streamer.NewForTesting()
	store := newTestStore(t)
	downloadDir := t.TempDir()

	d, _ := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "m", Name: "Gone"})
	_ = store.SetStatus(1, d.ID, StatusCompleted)
	_ = store.SetFilePath(1, d.ID, filepath.Join(downloadDir, "missing.mkv")) // does not exist

	// dataDir has no "Gone" folder → no cache source
	_ = NewWorker(WorkerConfig{Store: store, Streamer: s, DataDir: t.TempDir(), DownloadDir: downloadDir})

	got, _ := store.Get(1, d.ID)
	if got.Status != StatusCompleted {
		t.Errorf("no cache source → must NOT re-queue, got %q", got.Status)
	}
}

func TestMoveCompletedFile(t *testing.T) {
	store := newTestStore(t)
	s := streamer.NewForTesting()
	dataDir := t.TempDir()
	downloadDir := t.TempDir()
	w := NewWorker(WorkerConfig{
		Store: store, Streamer: s, DataDir: dataDir, DownloadDir: downloadDir,
		ResolveUsername: func(int) string { return "alice" },
	})
	d, err := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "m", Name: "T"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// anacrolix left a complete ".part" in the cache (not renamed yet)
	srcDir := filepath.Join(dataDir, "T")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "f.mkv.part"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.moveCompletedFile(*d, "T/f.mkv", "T", nil); err != nil {
		t.Fatalf("moveCompletedFile: %v", err)
	}
	// moved to downloadDir/{user}/{torrent}/file (final name, no .part)
	want := filepath.Join(downloadDir, "alice", "T", "f.mkv")
	if !fileExists(want) {
		t.Errorf("file should be moved to %q", want)
	}
	got, _ := store.Get(1, d.ID)
	if got.FilePath != want {
		t.Errorf("file_path = %q, want %q", got.FilePath, want)
	}
	// error path: source missing for this relPath
	if _, err := w.moveCompletedFile(*d, "Nope/x.mkv", "Nope", nil); err == nil {
		t.Error("expected error when source is missing")
	}
}
