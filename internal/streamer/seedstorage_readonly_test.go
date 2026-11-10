package streamer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// relocatedFixture writes content to a file outside DataDir and returns a
// streamer whose resolver points file 0 at it, plus a single-file info whose
// piece hashes describe `want` (which may differ from what is on disk).
func relocatedFixture(t *testing.T, onDisk, want []byte) (*Streamer, *metainfo.Info, string) {
	t.Helper()
	real := filepath.Join(t.TempDir(), "bulk", "a.mkv")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, onDisk, 0o644); err != nil {
		t.Fatal(err)
	}
	info := &metainfo.Info{Name: "a.mkv", PieceLength: 16}
	src := filepath.Join(t.TempDir(), "a.mkv")
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := info.BuildFromFilePath(src); err != nil {
		t.Fatal(err)
	}
	s := &Streamer{
		cfg:              Config{DataDir: t.TempDir()},
		filePathResolver: func(metainfo.Hash, int) (string, bool) { return real, true },
	}
	return s, info, real
}

func openRelocatedPiece(t *testing.T, s *Streamer, info *metainfo.Info) storage.PieceImpl {
	t.Helper()
	st := s.relocatedStorage(info, metainfo.Hash{})
	if st == nil {
		t.Fatal("expected relocated storage")
	}
	ti, err := st.OpenTorrent(context.Background(), info, metainfo.Hash{})
	if err != nil {
		t.Fatalf("open torrent: %v", err)
	}
	t.Cleanup(func() {
		if ti.Close != nil {
			_ = ti.Close()
		}
	})
	p := info.Piece(0)
	if ti.PieceWithHash != nil {
		h := p.V1Hash().Unwrap()
		return ti.PieceWithHash(p, g.Some(h[:]))
	}
	return ti.Piece(p)
}

// Regression: a relocated file whose bytes do not match the metainfo (a foreign
// or edited file) must never be overwritten by chunks fetched from peers.
func TestRelocatedStorage_RefusesWrites(t *testing.T) {
	onDisk := bytes.Repeat([]byte("F"), 32) // foreign content
	want := bytes.Repeat([]byte("T"), 32)   // what the torrent describes
	s, info, real := relocatedFixture(t, onDisk, want)

	piece := openRelocatedPiece(t, s, info)
	n, err := piece.WriteAt(want[:16], 0)
	if !errors.Is(err, errRelocatedReadOnly) || n != 0 {
		t.Fatalf("WriteAt = (%d, %v), want (0, errRelocatedReadOnly)", n, err)
	}
	got, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, onDisk) {
		t.Fatalf("relocated file was modified: %q", got)
	}
	if _, err := os.Stat(real + ".part"); !os.IsNotExist(err) {
		t.Fatalf("a .part file was created next to the relocated file: %v", err)
	}
}

// Reads (verification + seeding) still reach the real file.
func TestRelocatedStorage_ReadsPassThrough(t *testing.T) {
	data := bytes.Repeat([]byte("D"), 32)
	s, info, _ := relocatedFixture(t, data, data)

	piece := openRelocatedPiece(t, s, info)
	buf := make([]byte, 16)
	if n, err := piece.ReadAt(buf, 0); err != nil || n != 16 {
		t.Fatalf("ReadAt = (%d, %v)", n, err)
	}
	if !bytes.Equal(buf, data[:16]) {
		t.Fatalf("read %q", buf)
	}
	if err := piece.MarkComplete(); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	if c := piece.Completion(); !c.Ok || !c.Complete {
		t.Fatalf("completion after MarkComplete = %+v", c)
	}
}

func TestReadOnlyClient_CloseForwards(t *testing.T) {
	if err := (readOnlyClient{inner: noCloseClient{}, relocated: newFileIndexSet()}).Close(); err != nil {
		t.Fatalf("Close without inner closer: %v", err)
	}
	st := storage.NewFileOpts(storage.NewFileClientOpts{ClientBaseDir: t.TempDir(), PieceCompletion: storage.NewMapPieceCompletion()})
	if err := (readOnlyClient{inner: st}).Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestReadOnlyClient_OpenErrorPropagates(t *testing.T) {
	want := errors.New("boom")
	_, err := readOnlyClient{inner: noCloseClient{err: want}}.OpenTorrent(context.Background(), &metainfo.Info{}, metainfo.Hash{})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}

type noCloseClient struct{ err error }

func (c noCloseClient) OpenTorrent(context.Context, *metainfo.Info, metainfo.Hash) (storage.TorrentImpl, error) {
	return storage.TorrentImpl{}, c.err
}

// Only pieces overlapping a relocated file are locked; a file the resolver could
// not map falls back to DataDir and must stay writable (e.g. an unselected file
// of a partially downloaded multi-file torrent, fetched later on demand).
func TestLockedPieces_OnlyRelocatedFiles(t *testing.T) {
	info := &metainfo.Info{
		PieceLength: 10,
		Pieces:      make([]byte, 20*4), // 4 pieces: [0,10) [10,20) [20,30) [30,40)
		Files: []metainfo.FileInfo{
			{Length: 15, Path: []string{"a"}}, // pieces 0-1
			{Length: 0, Path: []string{"empty"}},
			{Length: 10, Path: []string{"b"}}, // pieces 1-2
			{Length: 15, Path: []string{"c"}}, // pieces 2-3
		},
	}
	set := newFileIndexSet()
	set.add(0)
	set.add(1) // zero-length file must not lock anything
	got := lockedPieces(info, set)
	want := []bool{true, true, false, false}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("locked = %v, want %v", got, want)
		}
	}
	if l := lockedPieces(info, nil); l[0] || l[3] {
		t.Fatalf("nil set must lock nothing: %v", l)
	}
}

// End to end through relocatedStorage: file 0 relocated (read-only), file 1
// unresolved → DataDir fallback, writable.
func TestRelocatedStorage_FallbackFileStaysWritable(t *testing.T) {
	real := filepath.Join(t.TempDir(), "a.mkv")
	if err := os.WriteFile(real, bytes.Repeat([]byte("A"), 16), 0o644); err != nil {
		t.Fatal(err)
	}
	info := &metainfo.Info{
		Name: "T", PieceLength: 16, Pieces: make([]byte, 20*2),
		Files: []metainfo.FileInfo{{Length: 16, Path: []string{"a.mkv"}}, {Length: 16, Path: []string{"b.mkv"}}},
	}
	s := &Streamer{
		cfg: Config{DataDir: t.TempDir()},
		filePathResolver: func(_ metainfo.Hash, idx int) (string, bool) {
			return real, idx == 0
		},
	}
	ti, err := s.relocatedStorage(info, metainfo.Hash{}).OpenTorrent(context.Background(), info, metainfo.Hash{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ti.Close() }()
	piece := func(i int) storage.PieceImpl {
		p := info.Piece(i)
		if ti.PieceWithHash != nil {
			return ti.PieceWithHash(p, g.None[[]byte]())
		}
		return ti.Piece(p)
	}
	if _, err := piece(0).WriteAt([]byte("x"), 0); !errors.Is(err, errRelocatedReadOnly) {
		t.Fatalf("relocated piece write err = %v", err)
	}
	if _, err := piece(1).WriteAt([]byte("y"), 0); err != nil {
		t.Fatalf("fallback piece must stay writable: %v", err)
	}
}

// A storage exposing only the legacy Piece accessor is wrapped too.
func TestReadOnlyClient_WrapsLegacyPiece(t *testing.T) {
	info := &metainfo.Info{PieceLength: 4, Pieces: make([]byte, 20), Files: []metainfo.FileInfo{{Length: 4, Path: []string{"a"}}}}
	set := newFileIndexSet()
	set.add(0)
	inner := pieceOnlyClient{}
	ti, err := readOnlyClient{inner: inner, relocated: set}.OpenTorrent(context.Background(), info, metainfo.Hash{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ti.Piece(info.Piece(0)).WriteAt([]byte("x"), 0); !errors.Is(err, errRelocatedReadOnly) {
		t.Fatalf("legacy Piece write err = %v", err)
	}
}

type pieceOnlyClient struct{}

func (pieceOnlyClient) OpenTorrent(context.Context, *metainfo.Info, metainfo.Hash) (storage.TorrentImpl, error) {
	return storage.TorrentImpl{Piece: func(metainfo.Piece) storage.PieceImpl { return readOnlyPiece{} }}, nil
}

func TestRelocatedPieceCompletion(t *testing.T) {
	if _, ok := (&Streamer{}).relocatedPieceCompletion().(interface{ Close() error }); !ok {
		t.Fatal("fallback completion must be usable")
	}
	pc, err := storage.NewBoltPieceCompletion(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if got := (&Streamer{dlPieceCompletion: pc}).relocatedPieceCompletion(); got != pc {
		t.Fatal("must reuse the shared persistent completion DB")
	}
}
