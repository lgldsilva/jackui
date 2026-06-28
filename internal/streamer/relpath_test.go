package streamer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

func relpathHash(t *testing.T, hex string) metainfo.Hash {
	t.Helper()
	var h metainfo.Hash
	if err := h.FromHexString(hex); err != nil {
		t.Fatalf("FromHexString: %v", err)
	}
	return h
}

func TestFileRelPath_FromMetadataCache(t *testing.T) {
	const hex = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s := NewForTesting()
	mc, err := NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	t.Cleanup(func() { _ = mc.Close() })
	s.SetMetadataCache(mc)
	if err := mc.Set(&TorrentInfo{
		InfoHash: hex, Name: "Pack",
		Files: []FileInfo{
			{Index: 0, Path: "Pack/Sub/a.mkv", Size: 4},
			{Index: 1, Path: "Pack/b.mkv", Size: 4},
		},
	}); err != nil {
		t.Fatalf("cache Set: %v", err)
	}

	h := relpathHash(t, hex)
	if got := s.FileRelPath(h, 1); got != "Pack/b.mkv" {
		t.Fatalf("FileRelPath(1) = %q, want Pack/b.mkv", got)
	}
	if got := s.FileRelPath(h, 0); got != "Pack/Sub/a.mkv" {
		t.Fatalf("FileRelPath(0) = %q, want Pack/Sub/a.mkv", got)
	}
	// Out-of-range index in the cache → falls through to the (absent) metainfo
	// and resolves empty.
	if got := s.FileRelPath(h, 9); got != "" {
		t.Fatalf("FileRelPath(9) = %q, want empty", got)
	}
	// Negative indices (sentinels) never resolve a rel path.
	if got := s.FileRelPath(h, -2); got != "" {
		t.Fatalf("FileRelPath(-2) = %q, want empty", got)
	}
	// Nil receiver stays safe (degraded handler paths).
	var nilS *Streamer
	if got := nilS.FileRelPath(h, 0); got != "" {
		t.Fatalf("nil receiver = %q, want empty", got)
	}
}

// writeTestMetainfo persists a .torrent into dir keyed by its info hash, the
// way persistMetainfo does, returning the hash.
func writeTestMetainfo(t *testing.T, dir string, info metainfo.Info) metainfo.Hash {
	t.Helper()
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("bencode.Marshal: %v", err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: infoBytes}
	h := mi.HashInfoBytes()
	f, err := os.Create(filepath.Join(dir, h.HexString()+".torrent"))
	if err != nil {
		t.Fatalf("create .torrent: %v", err)
	}
	if err := mi.Write(f); err != nil {
		t.Fatalf("write .torrent: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close .torrent: %v", err)
	}
	return h
}

func TestFileRelPath_FromCachedMetainfo(t *testing.T) {
	dir := t.TempDir()
	s := NewForTesting()
	s.metainfoDir = dir // no metadata-cache row → falls back to the .torrent

	piece := metainfo.HashBytes(bytes.Repeat([]byte("z"), 4))
	multi := writeTestMetainfo(t, dir, metainfo.Info{
		Name: "Pack", PieceLength: 1 << 14, Pieces: piece[:],
		Files: []metainfo.FileInfo{
			{Path: []string{"Sub", "a.mkv"}, Length: 4},
			{Path: []string{"b.mkv"}, Length: 4},
		},
	})
	single := writeTestMetainfo(t, dir, metainfo.Info{
		Name: "Solo.mkv", PieceLength: 1 << 14, Pieces: piece[:], Length: 4,
	})

	// Multi-file: name prefix + in-torrent path, anacrolix File.Path() format.
	if got := s.FileRelPath(multi, 0); got != "Pack/Sub/a.mkv" {
		t.Fatalf("multi(0) = %q, want Pack/Sub/a.mkv", got)
	}
	if got := s.FileRelPath(multi, 1); got != "Pack/b.mkv" {
		t.Fatalf("multi(1) = %q, want Pack/b.mkv", got)
	}
	// Single-file: the rel path IS the name.
	if got := s.FileRelPath(single, 0); got != "Solo.mkv" {
		t.Fatalf("single(0) = %q, want Solo.mkv", got)
	}
	// Out of range / unknown hash → empty.
	if got := s.FileRelPath(multi, 5); got != "" {
		t.Fatalf("multi(5) = %q, want empty", got)
	}
	unknown := relpathHash(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if got := s.FileRelPath(unknown, 0); got != "" {
		t.Fatalf("unknown hash = %q, want empty", got)
	}
}

func TestFileRelPath_CorruptMetainfoResolvesEmpty(t *testing.T) {
	dir := t.TempDir()
	s := NewForTesting()
	s.metainfoDir = dir
	h := relpathHash(t, "cccccccccccccccccccccccccccccccccccccccc")
	if err := os.WriteFile(filepath.Join(dir, h.HexString()+".torrent"), []byte("not bencode"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.FileRelPath(h, 0); got != "" {
		t.Fatalf("corrupt metainfo = %q, want empty", got)
	}
}
