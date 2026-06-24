package streamer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func TestDownloadTorrentDir(t *testing.T) {
	// nil sanitize → raw name appended.
	if got := downloadTorrentDir("/bulk/alice", "Movie 2026", nil); got != "/bulk/alice/Movie 2026" {
		t.Errorf("nil sanitize: got %q", got)
	}
	// sanitize applied to the name segment only (not the base).
	san := func(s string) string { return strings.ReplaceAll(s, "/", "_") }
	if got := downloadTorrentDir("/bulk/alice", "Dir/With/Slashes", san); got != "/bulk/alice/Dir_With_Slashes" {
		t.Errorf("sanitize: got %q", got)
	}
}

// buildInfoBytes makes an info-complete single-piece torrent and returns the
// info + its hash, so OpenTorrent yields a usable storage we can write to.
func buildInfoBytes(t *testing.T, info metainfo.Info) (*metainfo.Info, metainfo.Hash) {
	t.Helper()
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: ib}
	parsed, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	return &parsed, mi.HashInfoBytes()
}

func TestDownloadStorage_SingleFilePath(t *testing.T) {
	s := NewForTesting()
	base := t.TempDir()
	const piece = 1 << 14
	data := bytes.Repeat([]byte("z"), piece)
	ph := metainfo.HashBytes(data)
	info, hash := buildInfoBytes(t, metainfo.Info{
		Name: "Movie.mkv", PieceLength: piece, Length: int64(len(data)), Pieces: ph[:],
	})

	ci := s.downloadStorage(DownloadStorageSpec{BaseDir: base})
	to, err := ci.OpenTorrent(context.Background(), info, hash)
	if err != nil {
		t.Fatalf("OpenTorrent: %v", err)
	}
	defer func() { _ = to.Close() }()
	if _, err := to.Piece(info.Piece(0)).WriteAt(data, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if err := to.Piece(info.Piece(0)).MarkComplete(); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	// single-file → baseDir/<name>/<name>, matching moveDownloadedFile's
	// destDir/<base(relPath)>.
	want := filepath.Join(base, "Movie.mkv", "Movie.mkv")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected single file at %q: %v", want, err)
	}
}

func TestDownloadStorage_MultiFilePath(t *testing.T) {
	s := NewForTesting()
	base := t.TempDir()
	const piece = 1 << 14
	data := bytes.Repeat([]byte("m"), piece)
	ph := metainfo.HashBytes(data)
	// Multi-file torrent: name is the dir; file lives under S01/E01.mkv.
	info, hash := buildInfoBytes(t, metainfo.Info{
		Name:        "Pack",
		PieceLength: piece,
		Pieces:      ph[:],
		Files: []metainfo.FileInfo{
			{Length: int64(len(data)), Path: []string{"S01", "E01.mkv"}},
		},
	})

	ci := s.downloadStorage(DownloadStorageSpec{BaseDir: base})
	to, err := ci.OpenTorrent(context.Background(), info, hash)
	if err != nil {
		t.Fatalf("OpenTorrent: %v", err)
	}
	defer func() { _ = to.Close() }()
	if _, err := to.Piece(info.Piece(0)).WriteAt(data, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if err := to.Piece(info.Piece(0)).MarkComplete(); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	// multi-file → baseDir/<name>/<internal tree> (no duplicated name root).
	want := filepath.Join(base, "Pack", "S01", "E01.mkv")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected multi-file at %q: %v", want, err)
	}
}

func TestDownloadStorage_SanitizeApplied(t *testing.T) {
	s := NewForTesting()
	base := t.TempDir()
	const piece = 1 << 14
	data := bytes.Repeat([]byte("z"), piece)
	ph := metainfo.HashBytes(data)
	info, hash := buildInfoBytes(t, metainfo.Info{
		Name: "Bad:Name", PieceLength: piece, Length: int64(len(data)), Pieces: ph[:],
	})

	ci := s.downloadStorage(DownloadStorageSpec{
		BaseDir:  base,
		Sanitize: func(string) string { return "clean" },
	})
	to, err := ci.OpenTorrent(context.Background(), info, hash)
	if err != nil {
		t.Fatalf("OpenTorrent: %v", err)
	}
	defer func() { _ = to.Close() }()
	if _, err := to.Piece(info.Piece(0)).WriteAt(data, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if err := to.Piece(info.Piece(0)).MarkComplete(); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	want := filepath.Join(base, "clean", "Bad:Name")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected sanitized dir, file at %q: %v", want, err)
	}
}
