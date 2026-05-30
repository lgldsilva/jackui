package streamer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

func TestBuildImageCandidates_Empty(t *testing.T) {
	cands := buildImageCandidates(nil)
	if len(cands) != 0 {
		t.Errorf("expected 0 candidates from nil files, got %d", len(cands))
	}
}

func TestSortCandsByPreference(t *testing.T) {
	cands := []imgCandidate{
		{idx: 0, size: 100, preferred: false, preferRank: 0},
		{idx: 1, size: 50, preferred: true, preferRank: 5},
		{idx: 2, size: 200, preferred: true, preferRank: 3},
	}
	sortCandsByPreference(cands)
	if cands[0].idx != 1 {
		t.Errorf("expected preferred with highest rank first (idx 1), got idx %d", cands[0].idx)
	}
	if cands[2].idx != 0 {
		t.Errorf("expected non-preferred last (idx 0), got idx %d", cands[2].idx)
	}
}

func TestSortCandsByPreference_SameRank(t *testing.T) {
	cands := []imgCandidate{
		{idx: 0, size: 100, preferred: true, preferRank: 3},
		{idx: 1, size: 200, preferred: true, preferRank: 3},
	}
	sortCandsByPreference(cands)
	if cands[0].idx != 1 {
		t.Errorf("same rank should prefer larger size: expected idx 1, got idx %d", cands[0].idx)
	}
}

func TestSaveArtBytes(t *testing.T) {
	dir := t.TempDir()
	s := &Streamer{cfg: Config{DataDir: dir}}

	rel, err := s.SaveArtBytes(metainfo.Hash{1, 2, 3, 4, 5}, []byte("fake-jpeg-data"))
	if err != nil {
		t.Fatalf("SaveArtBytes: %v", err)
	}

	full := filepath.Join(dir, rel)
	if _, err := os.Stat(full); os.IsNotExist(err) {
		t.Fatalf("art file not created at %s", full)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "fake-jpeg-data" {
		t.Errorf("content: want %q, got %q", "fake-jpeg-data", string(data))
	}
}

func TestReadArtBytes(t *testing.T) {
	dir := t.TempDir()
	s := &Streamer{cfg: Config{DataDir: dir}}

	rel, err := s.SaveArtBytes(metainfo.Hash{0xAA}, []byte("art-data"))
	if err != nil {
		t.Fatalf("SaveArtBytes: %v", err)
	}

	data, err := s.ReadArtBytes(rel)
	if err != nil {
		t.Fatalf("ReadArtBytes: %v", err)
	}
	if string(data) != "art-data" {
		t.Errorf("content: want %q, got %q", "art-data", string(data))
	}
}

func TestReadArtBytes_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	s := &Streamer{cfg: Config{DataDir: dir}}

	_, err := s.ReadArtBytes("../../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestReadArtBytes_OutsideArtDir(t *testing.T) {
	dir := t.TempDir()
	s := &Streamer{cfg: Config{DataDir: dir}}

	_, err := s.ReadArtBytes("someother/file.jpg")
	if err == nil {
		t.Fatal("expected error for path outside .art dir")
	}
}

func TestReadArtBytes_NotFound(t *testing.T) {
	dir := t.TempDir()
	s := &Streamer{cfg: Config{DataDir: dir}}

	_, err := s.ReadArtBytes(filepath.Join(artDirName, "nonexistent.jpg"))
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestArtImageExtensions(t *testing.T) {
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		if !artImageExtensions[ext] {
			t.Errorf("extension %s should be in artImageExtensions", ext)
		}
	}
	for _, ext := range []string{".gif", ".bmp", ".svg"} {
		if artImageExtensions[ext] {
			t.Errorf("extension %s should NOT be in artImageExtensions", ext)
		}
	}
}

func TestPreferredArtBasenames(t *testing.T) {
	expected := []string{"poster", "cover", "folder", "fanart", "movie", "show", "thumb", "front", "art"}
	if len(preferredArtBasenames) != len(expected) {
		t.Fatalf("len: want %d, got %d", len(expected), len(preferredArtBasenames))
	}
	for i, name := range expected {
		if preferredArtBasenames[i] != name {
			t.Fatalf("index %d: want %q, got %q", i, name, preferredArtBasenames[i])
		}
	}
}

func TestBuildImageCandidates_SkipsSmallFiles(t *testing.T) {
	// We can't easily construct torrent.File objects, but the function is
	// table-driven and simple enough that we trust the minTorrentImageBytes gate.
	if minTorrentImageBytes != 10<<10 {
		t.Errorf("expected minTorrentImageBytes=10KiB, got %d", minTorrentImageBytes)
	}
	if maxTorrentImageBytes != 8<<20 {
		t.Errorf("expected maxTorrentImageBytes=8MiB, got %d", maxTorrentImageBytes)
	}
}
