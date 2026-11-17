package streamer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// manyFileInfo builds an n-file torrent (one 16-byte piece per file) and writes
// every file under bulk, returning the info and the real path per index.
func manyFileInfo(t *testing.T, bulk string, n int) (*metainfo.Info, []string) {
	t.Helper()
	info := &metainfo.Info{Name: "Pack", PieceLength: 16, Pieces: make([]byte, 20*n)}
	paths := make([]string, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("f%04d.bin", i)
		info.Files = append(info.Files, metainfo.FileInfo{Length: 16, Path: []string{"sub", name}})
		paths[i] = filepath.Join(bulk, "sub", name)
	}
	if err := os.MkdirAll(filepath.Join(bulk, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if err := os.WriteFile(p, bytes.Repeat([]byte("A"), 16), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return info, paths
}

func openPiece(ti storage.TorrentImpl, info *metainfo.Info, i int) storage.PieceImpl {
	p := info.Piece(i)
	if ti.PieceWithHash != nil {
		return ti.PieceWithHash(p, g.None[[]byte]())
	}
	return ti.Piece(p)
}

// Pins the fix for the O(files) resolver round-trips at activation: opening a
// relocated torrent must cost ONE layout resolution, however many files it has,
// and never fall back to the per-file resolver (which costs a metadata decode
// plus up to two queries per call in production).
func TestRelocatedStorage_ResolvesLayoutOnce(t *testing.T) {
	const n = 300
	bulk := t.TempDir()
	info, paths := manyFileInfo(t, bulk, n)

	var layoutCalls, perFileCalls int
	var gotRel []string
	s := &Streamer{cfg: Config{DataDir: t.TempDir()}}
	s.SetFileLayoutResolver(func(_ metainfo.Hash, relPaths []string) map[int]string {
		layoutCalls++
		gotRel = relPaths
		out := make(map[int]string, len(paths))
		for i, p := range paths {
			out[i] = p
		}
		return out
	})
	s.SetFilePathResolver(func(_ metainfo.Hash, idx int) (string, bool) {
		perFileCalls++
		return paths[idx], true
	})

	st := s.relocatedStorage(info, metainfo.Hash{})
	if st == nil {
		t.Fatal("expected a relocated storage")
	}
	ti, err := st.OpenTorrent(context.Background(), info, metainfo.Hash{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ti.Close() }()

	if layoutCalls != 1 || perFileCalls != 0 {
		t.Fatalf("resolver calls for %d files: layout=%d per-file=%d, want layout=1 per-file=0",
			n, layoutCalls, perFileCalls)
	}
	if len(gotRel) != n || gotRel[0] != "Pack/sub/f0000.bin" || gotRel[n-1] != "Pack/sub/f0299.bin" {
		t.Fatalf("relPaths = %d entries, first %q", len(gotRel), gotRel[0])
	}
	// Every file was mapped to its real path: each piece reads the bulk bytes
	// and refuses writes.
	for _, i := range []int{0, n / 2, n - 1} {
		buf := make([]byte, 16)
		if _, err := openPiece(ti, info, i).ReadAt(buf, 0); err != nil || !bytes.Equal(buf, bytes.Repeat([]byte("A"), 16)) {
			t.Fatalf("piece %d read = %q, %v", i, buf, err)
		}
		if _, err := openPiece(ti, info, i).WriteAt([]byte("x"), 0); !errors.Is(err, errRelocatedReadOnly) {
			t.Fatalf("piece %d write err = %v", i, err)
		}
	}
}

// A layout entry must be an existing regular file: a stale row (missing file,
// directory, empty path) falls back to DataDir instead of rooting the torrent
// file somewhere it cannot be read.
func TestRelocatedLayout_KeepsOnlyRegularFiles(t *testing.T) {
	bulk := t.TempDir()
	info, paths := manyFileInfo(t, bulk, 5)
	s := &Streamer{cfg: Config{DataDir: t.TempDir()}}
	s.SetFileLayoutResolver(func(metainfo.Hash, []string) map[int]string {
		return map[int]string{
			0: paths[0],
			1: filepath.Join(bulk, "ghost.bin"),
			2: bulk,
			3: "",
		}
	})
	layout := s.relocatedLayout(info, metainfo.Hash{})
	if len(layout) != 1 || layout[0] != paths[0] {
		t.Fatalf("layout = %v, want only file 0", layout)
	}

	ti, err := s.relocatedStorage(info, metainfo.Hash{}).OpenTorrent(context.Background(), info, metainfo.Hash{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ti.Close() }()
	if _, err := openPiece(ti, info, 0).WriteAt([]byte("x"), 0); !errors.Is(err, errRelocatedReadOnly) {
		t.Fatalf("relocated piece write err = %v", err)
	}
	if _, err := openPiece(ti, info, 1).WriteAt([]byte("y"), 0); err != nil {
		t.Fatalf("unresolved file must stay writable under DataDir: %v", err)
	}
}

func TestRelocatedLayout_NilCases(t *testing.T) {
	info := twoFileInfo()
	var h metainfo.Hash
	s := &Streamer{cfg: Config{DataDir: t.TempDir()}}
	if s.relocatedLayout(nil, h) != nil || s.relocatedLayout(&metainfo.Info{Name: "E", Files: []metainfo.FileInfo{}}, h) != nil {
		t.Fatal("no info / no files must yield no layout")
	}
	if s.relocatedLayout(info, h) != nil {
		t.Fatal("no resolver must yield no layout")
	}
	// Batch resolver that knows nothing (DB error, unknown hash) → default storage.
	s.SetFileLayoutResolver(func(metainfo.Hash, []string) map[int]string { return nil })
	if s.relocatedStorage(info, h) != nil {
		t.Fatal("an empty layout must not relocate")
	}
	// File 0 unresolved by the batch resolver → default storage, as before.
	real := filepath.Join(t.TempDir(), "b.mkv")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.SetFileLayoutResolver(func(metainfo.Hash, []string) map[int]string { return map[int]string{1: real} })
	if s.relocatedStorage(info, h) != nil {
		t.Fatal("file 0 unresolved must not relocate")
	}
}

// Without the batch resolver the per-file one still drives the layout, and a
// miss on file 0 stops before resolving the rest.
func TestRelocatedLayout_PerFileFallback(t *testing.T) {
	bulk := t.TempDir()
	info, paths := manyFileInfo(t, bulk, 4)
	calls := 0
	s := &Streamer{cfg: Config{DataDir: t.TempDir()}}
	s.SetFilePathResolver(func(_ metainfo.Hash, idx int) (string, bool) {
		calls++
		return paths[idx], idx != 2
	})
	layout := s.relocatedLayout(info, metainfo.Hash{})
	if len(layout) != 3 || layout[0] != paths[0] || layout[3] != paths[3] || layout[2] != "" {
		t.Fatalf("layout = %v", layout)
	}
	if calls != 4 {
		t.Fatalf("per-file calls = %d, want one per file", calls)
	}

	calls = 0
	s.SetFilePathResolver(func(metainfo.Hash, int) (string, bool) { calls++; return "", false })
	if s.relocatedLayout(info, metainfo.Hash{}) != nil || calls != 1 {
		t.Fatalf("file 0 miss: calls = %d, want 1 and no layout", calls)
	}
}

func TestInfoRelPaths(t *testing.T) {
	multi := twoFileInfo()
	got := infoRelPaths(multi, multi.UpvertedFiles())
	if len(got) != 2 || got[0] != "MyTorrent/a.mkv" || got[1] != "MyTorrent/subdir/b.mkv" {
		t.Fatalf("multi-file rel paths = %v", got)
	}
	// Single-file torrents have no leading directory: the rel path IS the name.
	single := &metainfo.Info{Name: "Solo.mkv", Length: 10, PieceLength: 16}
	if got := infoRelPaths(single, single.UpvertedFiles()); len(got) != 1 || got[0] != "Solo.mkv" {
		t.Fatalf("single-file rel paths = %v", got)
	}
}

// The path maker must not panic on a request it cannot map, and must send it to
// the DataDir layout.
func TestLayoutFilePathMaker_UnknownFileFallsBack(t *testing.T) {
	info := twoFileInfo()
	s := &Streamer{cfg: Config{DataDir: "/cache"}}
	resolved := 0
	mk := s.layoutFilePathMaker(info, map[int]string{0: "/bulk/a.mkv"}, func(int) { resolved++ })
	files := info.UpvertedFiles()
	if got := mk(storage.FilePathMakerOpts{Info: info, File: &files[0]}); got != "bulk/a.mkv" || resolved != 1 {
		t.Fatalf("mapped file = %q (resolved %d)", got, resolved)
	}
	want := filepath.Join("cache", "MyTorrent", "subdir", "b.mkv")
	if got := mk(storage.FilePathMakerOpts{Info: info, File: &files[1]}); got != want || resolved != 1 {
		t.Fatalf("unmapped file = %q, want %q", got, want)
	}
	other := metainfo.FileInfo{Path: []string{"nope.mkv"}}
	if got := mk(storage.FilePathMakerOpts{Info: info, File: &other}); got != filepath.Join("cache", "MyTorrent", "nope.mkv") {
		t.Fatalf("unknown file = %q", got)
	}
	// A nil onResolved is allowed.
	if got := s.layoutFilePathMaker(info, map[int]string{0: "/bulk/a.mkv"}, nil)(storage.FilePathMakerOpts{Info: info, File: &files[0]}); got != "bulk/a.mkv" {
		t.Fatalf("nil onResolved = %q", got)
	}
}
