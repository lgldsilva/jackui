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

// Downloads group under their category folder by default (…/<user>/<category>/…)
// so the browser isn't a flat dump. A picked destination still wins (no grouping).
func TestCompletionBaseDir_GroupsByCategory(t *testing.T) {
	w, _ := newBulkWorker(t, "/dl", "", false)
	if got := w.completionBaseDir(Download{UserID: 1, Category: "Movies"}); got != filepath.FromSlash("/dl/alice/Movies") {
		t.Errorf("category group base = %q, want /dl/alice/Movies", got)
	}
	// No category → no grouping (back-compat).
	if got := w.completionBaseDir(Download{UserID: 1}); got != filepath.FromSlash("/dl/alice") {
		t.Errorf("no-category base = %q, want /dl/alice", got)
	}
	// Picked destination wins — category does NOT re-group it.
	if got := w.completionBaseDir(Download{UserID: 1, Category: "Movies", DestBase: "/mnt/nas"}); got != "/mnt/nas" {
		t.Errorf("DestBase base = %q, want /mnt/nas (no category grouping)", got)
	}
}

func TestCategoryFolder(t *testing.T) {
	cases := map[string]string{
		"":            "",
		"all":         "",
		"All":         "",
		"Movies":      "Movies",
		"TV/HD":       "TV",     // top-level only
		"Movies\\UHD": "Movies", // backslash subcategory
		"5000":        "",       // bare torznab id
		"XXX":         "XXX",
	}
	for in, want := range cases {
		if got := categoryFolder(in); got != want {
			t.Errorf("categoryFolder(%q) = %q, want %q", in, got, want)
		}
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

// The destination frozen at metadata-resolve wins over the recomputed one, so a
// later category/auto-promote change can't make the finalize look in the wrong dir.
func TestFinalizeBulk_PrefersFrozenCompletionDest(t *testing.T) {
	dl := t.TempDir()
	w, store := newBulkWorker(t, dl, "", false)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "m", Name: "Movie.mkv", Category: "Movies"})
	// Freeze a dir that differs from the current completionDest (category drift).
	frozen := filepath.Join(dl, "frozen", "Movie.mkv")
	if err := store.SetCompletionDest(1, d.ID, frozen); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(frozen, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(frozen, "Movie.mkv")
	if err := os.WriteFile(want, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	row, _ := store.Get(1, d.ID) // re-load so CompletionDest is populated
	got, ok := w.tryFinalizeBulk(*row, "Movie.mkv", []string{"Movie.mkv"}, false)
	if !ok || got != want {
		t.Fatalf("frozen dest not preferred: ok=%v got=%q want=%q", ok, got, want)
	}
}

// THE WEDGE FIX: a row created with a category whose storage wrote BEFORE category
// grouping shipped — the file lives at the category-LESS path, but the current
// completionDest points at .../<category>/<torrent> (empty). The category-less
// probe must still finalize it instead of wedging in `moving`.
func TestFinalizeBulk_FallbackCategoryLessForWedgedRow(t *testing.T) {
	dl := t.TempDir()
	w, store := newBulkWorker(t, dl, "", false)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "m", Name: "Movie.mkv", Category: "Movies"})
	legacy := filepath.Join(dl, "alice", "Movie.mkv") // no "Movies" segment
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(legacy, "Movie.mkv")
	if err := os.WriteFile(want, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Current completionDest is /dl/alice/Movies/Movie.mkv (empty) → must fall back.
	got, ok := w.tryFinalizeBulk(*d, "Movie.mkv", []string{"Movie.mkv"}, false)
	if !ok || got != want {
		t.Fatalf("category-less fallback failed: ok=%v got=%q want=%q", ok, got, want)
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
