package downloads

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/jackui/internal/streamer"
)

func newBulkWorker(t *testing.T, downloadDir, sharedDir string, autoPromoteArr bool) (*Worker, *Store) {
	t.Helper()
	store := newTestStore(t)
	cfg := WorkerConfig{
		Store:           store,
		Streamer:        streamer.NewForTesting(),
		DataDir:         t.TempDir(),
		DownloadDir:     downloadDir,
		SharedDir:       sharedDir,
		ResolveUsername: func(int) string { return "alice" },
		// MaxActive > 0 so queueSettings() honours this instead of the default.
		Settings: func() QueueSettings { return QueueSettings{MaxActive: 3, AutoPromoteArr: autoPromoteArr} },
	}
	return NewWorker(cfg), store
}

func TestCompletionBaseDir_PerUser(t *testing.T) {
	w, _ := newBulkWorker(t, "/dl", "", false)
	if got := w.completionBaseDir(Download{UserID: 1}); got != "/dl/alice" {
		t.Errorf("per-user base = %q, want /dl/alice", got)
	}
}

// When the username resolver transiently fails (returns ""), a per-user download
// must NOT fall to the bare downloadDir (the mount root the UserSubpath migration
// scans) — it falls back to FallbackUser so it stays scoped under a user subdir.
func TestCompletionBaseDir_FallbackUserWhenResolveFails(t *testing.T) {
	store := newTestStore(t)
	w := NewWorker(WorkerConfig{
		Store:           store,
		Streamer:        streamer.NewForTesting(),
		DataDir:         t.TempDir(),
		DownloadDir:     "/dl",
		ResolveUsername: func(int) string { return "" }, // transient failure
		FallbackUser:    "admin",
		Settings:        func() QueueSettings { return QueueSettings{MaxActive: 3} },
	})
	if got := w.completionBaseDir(Download{UserID: 7}); got != filepath.FromSlash("/dl/admin") {
		t.Errorf("resolve-fail base = %q, want /dl/admin (never the bare /dl root)", got)
	}
}

func TestCompletionBaseDir_NoDownloadDir(t *testing.T) {
	w, _ := newBulkWorker(t, "", "", false)
	if got := w.completionBaseDir(Download{UserID: 1}); got != "" {
		t.Errorf("no downloadDir → base should be empty, got %q", got)
	}
}

func TestCompletionBaseDir_Arr(t *testing.T) {
	w, _ := newBulkWorker(t, "/dl", "/shared", true)
	got := w.completionBaseDir(Download{UserID: 1, Source: SourceArr, Category: "Movies"})
	if got != "/shared/Movies" {
		t.Errorf("arr auto-promote base = %q, want /shared/Movies", got)
	}
}

// TestCompletionDest_EqualsBaseDirPlusSanitizedName is THE casamento invariant:
// the download-to-bulk storage roots a torrent at BaseDir/<sanitize(name)> and
// the move target is completionDest — they must be byte-identical or the
// move-on-completion stops being a no-op.
func TestCompletionDest_EqualsBaseDirPlusSanitizedName(t *testing.T) {
	w, _ := newBulkWorker(t, "/dl", "", false)
	d := Download{UserID: 1}
	for _, name := range []string{"Clean Name", "Bad/..\\name", "with:colon", "trailing."} {
		want := filepath.Join(w.completionBaseDir(d), sanitizeFolderName(name))
		if got := w.completionDest(d, name); got != want {
			t.Errorf("completionDest(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestCompletionBaseDir_PrefersChosenDestination(t *testing.T) {
	w, _ := newBulkWorker(t, "/dl", "/shared", true)
	// A picked destination wins over downloadDir AND over the *arr auto-promote.
	d := Download{UserID: 1, Source: SourceArr, Category: "Movies", DestBase: "/mnt/nas"}
	if got := w.completionBaseDir(d); got != "/mnt/nas" {
		t.Errorf("DestBase: got %q, want /mnt/nas", got)
	}
	d.DestSubdir = "Series/2026"
	if got := w.completionBaseDir(d); got != filepath.FromSlash("/mnt/nas/Series/2026") {
		t.Errorf("DestBase+subdir: got %q", got)
	}
	// A tampered traversal subdir is dropped (defense-in-depth).
	d.DestSubdir = "../../etc"
	if got := w.completionBaseDir(d); got != "/mnt/nas" {
		t.Errorf("traversal subdir should be dropped: got %q", got)
	}
}

func TestCleanDestSubdir(t *testing.T) {
	cases := map[string]string{
		"":          "",
		"movies":    "movies",
		"a/b/c":     filepath.FromSlash("a/b/c"),
		".":         "",
		"../x":      "",
		"a/../../x": "",
		"/abs/path": "",
	}
	for in, want := range cases {
		if got := cleanDestSubdir(in); got != want {
			t.Errorf("cleanDestSubdir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFinalizeBulkCompletion_SingleFile(t *testing.T) {
	dl := t.TempDir()
	w, store := newBulkWorker(t, dl, "", false)
	d, err := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "m", Name: "Movie.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	// The storage wrote the single file at completionDest/Movie.mkv.
	dest := filepath.Join(dl, "alice", "Movie.mkv")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dest, "Movie.mkv")
	if err := os.WriteFile(want, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := w.tryFinalizeBulk(*d, "Movie.mkv", []string{"Movie.mkv"}, false)
	if !ok {
		t.Fatal("expected ok=true (file present in bulk)")
	}
	if got != want {
		t.Errorf("dst = %q, want %q", got, want)
	}
	row, _ := store.Get(1, d.ID)
	if row.FilePath != want {
		t.Errorf("file_path = %q, want %q", row.FilePath, want)
	}
}

// The tricky case: a single file selected from a MULTI-file torrent. The storage
// preserves the internal tree (no name root), so finalize must look there — not
// at the flattened basename moveDownloadedFile would have used.
func TestFinalizeBulkCompletion_SelectedFileInMulti(t *testing.T) {
	dl := t.TempDir()
	w, store := newBulkWorker(t, dl, "", false)
	d, err := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 2, Magnet: "m", Name: "Pack"})
	if err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(dl, "alice", "Pack", "S01")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(destDir, "E01.mkv")
	if err := os.WriteFile(want, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := w.tryFinalizeBulk(*d, "Pack", []string{"Pack/S01/E01.mkv"}, false)
	if !ok {
		t.Fatal("expected ok=true (selected file present in bulk)")
	}
	if got != want {
		t.Errorf("dst = %q, want %q (tree preserved, name root stripped)", got, want)
	}
}

func TestFinalizeBulkCompletion_WholeTorrent(t *testing.T) {
	dl := t.TempDir()
	w, store := newBulkWorker(t, dl, "", false)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: -2, Magnet: "m", Name: "Pack"})
	destDir := filepath.Join(dl, "alice", "Pack")
	if err := os.MkdirAll(filepath.Join(destDir, "S01"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "S01", "E01.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := w.tryFinalizeBulk(*d, "Pack", []string{"Pack/S01/E01.mkv"}, true)
	if !ok {
		t.Fatal("expected ok=true (whole torrent dir present in bulk)")
	}
	if got != destDir {
		t.Errorf("whole dst = %q, want %q", got, destDir)
	}
}

func TestFinalizeBulkCompletion_MissingErrors(t *testing.T) {
	dl := t.TempDir()
	w, store := newBulkWorker(t, dl, "", false)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "m", Name: "Gone.mkv"})
	if _, ok := w.tryFinalizeBulk(*d, "Gone.mkv", []string{"Gone.mkv"}, false); ok {
		t.Error("expected ok=false when the bulk file is missing (falls back to move)")
	}
}
