package downloads

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/streamer"
)

// seedWholeCompleted creates a completed whole-torrent row whose file_path is
// destDir — exactly how the worker leaves it after moveCompletedTorrent.
func seedWholeCompleted(t *testing.T, s *Store, hash, name, destDir string) {
	t.Helper()
	d, err := s.Create(Download{
		UserID: 1, InfoHash: hash, FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:" + hash, Name: name,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.UpdateMetadata(1, d.ID, name, destDir, 0); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}
	if err := s.SetStatus(1, d.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
}

// mustWrite creates the file (and parents) under dir with tiny content.
func mustWrite(t *testing.T, dir string, parts ...string) string {
	t.Helper()
	p := filepath.Join(append([]string{dir}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The play-path case the whole-torrent feature broke: a torrent downloaded as
// ONE item has only the -2 row, so streaming a file by index must resolve into
// the moved tree instead of re-downloading from the (possibly dead) swarm.
func TestGetCompletedPathRel_WholeRowResolvesMovedFile(t *testing.T) {
	s := dlwNewStore(t)
	destDir := t.TempDir()
	want := mustWrite(t, destDir, "Sub", "a.mkv")
	seedWholeCompleted(t, s, "wh1", "Pack", destDir)

	got, err := s.GetCompletedPathRel("wh1", 0, "Pack/Sub/a.mkv")
	if err != nil {
		t.Fatalf("GetCompletedPathRel: %v", err)
	}
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestGetCompletedPathRel_SingleFileTorrentFormat(t *testing.T) {
	// Single-file torrents have no leading dir in File.Path() — the rel path IS
	// the file name, and the moved tree keeps it directly under destDir.
	s := dlwNewStore(t)
	destDir := t.TempDir()
	want := mustWrite(t, destDir, "Solo.mkv")
	seedWholeCompleted(t, s, "wh2", "Solo.mkv", destDir)

	got, err := s.GetCompletedPathRel("wh2", 0, "Solo.mkv")
	if err != nil {
		t.Fatalf("GetCompletedPathRel: %v", err)
	}
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestGetCompletedPathRel_PerFileRowWins(t *testing.T) {
	// Compat: a per-file completed row keeps the old semantics — its file_path
	// is returned as-is (no stat), exactly like GetCompletedPath.
	s := dlwNewStore(t)
	d, err := s.Create(Download{
		UserID: 1, InfoHash: "pf1", FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:pf1", Name: "Movie", FilePath: "/dl/Movie/movie.mkv",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetStatus(1, d.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got, err := s.GetCompletedPathRel("pf1", 0, "Movie/whatever.mkv")
	if err != nil {
		t.Fatalf("GetCompletedPathRel: %v", err)
	}
	if got != "/dl/Movie/movie.mkv" {
		t.Fatalf("path = %q, want the per-file row's file_path", got)
	}
}

func TestGetCompletedPathRel_RejectsTraversal(t *testing.T) {
	s := dlwNewStore(t)
	parent := t.TempDir()
	destDir := filepath.Join(parent, "Pack")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The escape target EXISTS — only the sanitization stands between the
	// metadata-supplied rel path and a file outside destDir.
	mustWrite(t, parent, "secret.txt")
	seedWholeCompleted(t, s, "wh3", "Pack", destDir)

	for _, rel := range []string{"Pack/../secret.txt", "../secret.txt", "/etc/passwd", "Pack/../../secret.txt"} {
		got, err := s.GetCompletedPathRel("wh3", 0, rel)
		if err != nil {
			t.Fatalf("GetCompletedPathRel(%q): %v", rel, err)
		}
		if got != "" {
			t.Fatalf("rel %q resolved to %q, want rejection", rel, got)
		}
	}
}

func TestGetCompletedPathRel_MissingFileResolvesEmpty(t *testing.T) {
	s := dlwNewStore(t)
	destDir := t.TempDir()
	seedWholeCompleted(t, s, "wh4", "Pack", destDir)

	got, err := s.GetCompletedPathRel("wh4", 0, "Pack/ghost.mkv")
	if err != nil || got != "" {
		t.Fatalf("got (%q, %v), want empty for a file missing from the tree", got, err)
	}
	// A rel path resolving to a DIRECTORY must not be served either.
	if err := os.MkdirAll(filepath.Join(destDir, "Sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetCompletedPathRel("wh4", 0, "Pack/Sub")
	if err != nil || got != "" {
		t.Fatalf("got (%q, %v), want empty for a directory", got, err)
	}
}

func TestGetCompletedPathRel_GuardsAndMisses(t *testing.T) {
	s := dlwNewStore(t)
	destDir := t.TempDir()
	mustWrite(t, destDir, "a.mkv")
	seedWholeCompleted(t, s, "wh5", "Pack", destDir)

	// Empty rel path → no whole fallback (caller had no metainfo).
	if got, err := s.GetCompletedPathRel("wh5", 0, ""); err != nil || got != "" {
		t.Fatalf("empty rel: got (%q, %v), want empty", got, err)
	}
	// Sentinel/negative indices never resolve through the whole fallback.
	if got, err := s.GetCompletedPathRel("wh5", FileIndexAuto, "Pack/a.mkv"); err != nil || got != "" {
		t.Fatalf("negative idx: got (%q, %v), want empty", got, err)
	}
	// Unknown hash → miss.
	if got, err := s.GetCompletedPathRel("nope", 0, "Pack/a.mkv"); err != nil || got != "" {
		t.Fatalf("unknown hash: got (%q, %v), want empty", got, err)
	}
	// Nil store stays nil-safe like GetCompletedPath.
	var nilS *Store
	if got, err := nilS.GetCompletedPathRel("wh5", 0, "Pack/a.mkv"); err != nil || got != "" {
		t.Fatalf("nil store: got (%q, %v), want empty", got, err)
	}
}

// ─── wholeTorrentDest: traversal hidden behind the name prefix ──────────────

func TestWholeTorrentDest_RejectsTraversalAfterStrip(t *testing.T) {
	// "Pack/../x" is lexically local as a whole (cleans to "x") but escapes the
	// destination once the leading "Pack/" is stripped — must be rejected.
	if _, err := wholeTorrentDest("/dl/Pack", "Pack", "Pack/../evil.bin"); err == nil {
		t.Fatal("expected rejection of post-strip traversal")
	}
	if _, err := wholeTorrentDest("/dl/Pack", "Pack", "Pack/Sub/../../../evil.bin"); err == nil {
		t.Fatal("expected rejection of deep post-strip traversal")
	}
}

// ─── pad files (BEP 47) are never moved ──────────────────────────────────────

func TestMoveCompletedTree_SkipsPadPaths(t *testing.T) {
	dataDir := t.TempDir()
	destDir := filepath.Join(t.TempDir(), "T")
	mustWrite(t, dataDir, "T", "real.bin")
	// Pad entries exist only in metadata — nothing on disk. Without the skip,
	// the move would fail forever ("completed file not found").
	err := moveCompletedTree(dataDir, destDir, "T", []string{"T/.pad/512", ".pad/1024", "T/real.bin"}, nil, nil)
	if err != nil {
		t.Fatalf("moveCompletedTree: %v", err)
	}
	if !fileExists(filepath.Join(destDir, "real.bin")) {
		t.Error("real.bin should have been moved")
	}
	if fileExists(filepath.Join(destDir, ".pad")) {
		t.Error(".pad entries must not be moved")
	}
}

func TestIsPadPath(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"T/.pad/512", true},
		{".pad/512", true},
		{"T/real.bin", false},
		{"T/Sub/.pad-not/512", false},
		{"T/padfile.bin", false},
	}
	for _, c := range cases {
		if got := isPadPath("T", c.rel); got != c.want {
			t.Errorf("isPadPath(T, %q) = %v, want %v", c.rel, got, c.want)
		}
	}
}

func TestMoveCompletedTorrent_SkipsPadAttrFiles(t *testing.T) {
	store := dlwNewStore(t)
	s := streamer.NewForTesting()
	dataDir := t.TempDir()
	downloadDir := t.TempDir()
	w := NewWorker(WorkerConfig{Store: store, Streamer: s, DataDir: dataDir, DownloadDir: downloadDir})
	d, err := store.Create(Download{
		UserID: 1, InfoHash: "pad1", FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:pad1", Name: "PadPack",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// One real file plus a BEP 47 pad entry (attr "p") that anacrolix never
	// materializes on disk — the move must succeed without it.
	tor := wholeSpecTorrentFI(t, "PadPack", []metainfo.FileInfo{
		{Path: []string{"real.bin"}, Length: 4},
		{Path: []string{".pad", "4"}, Length: 4, ExtendedFileAttrs: metainfo.ExtendedFileAttrs{Attr: "p"}},
	})
	mustWrite(t, dataDir, "PadPack", "real.bin")

	td := &trackedDL{id: d.ID, userID: 1, name: "PadPack", whole: tor}
	destDir, err := w.moveCompletedTorrent(*d, td)
	if err != nil {
		t.Fatalf("moveCompletedTorrent: %v", err)
	}
	if !fileExists(filepath.Join(destDir, "real.bin")) {
		t.Error("real.bin should exist at the destination")
	}
	if fileExists(filepath.Join(destDir, ".pad")) {
		t.Error("pad file must not be moved to the destination")
	}
}
