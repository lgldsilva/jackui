package streamer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

func TestSaveArtBytes(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()

	h := metainfo.Hash{0x01, 0x02, 0x03}
	data := []byte("fake jpeg bytes")

	rel, err := s.SaveArtBytes(h, data)
	if err != nil {
		t.Fatalf("SaveArtBytes: %v", err)
	}
	if rel == "" {
		t.Fatal("expected non-empty relative path")
	}

	got, err := s.ReadArtBytes(rel)
	if err != nil {
		t.Fatalf("ReadArtBytes: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("ReadArtBytes: got %q, want %q", got, data)
	}

	full := filepath.Join(s.cfg.DataDir, rel)
	info, err := os.Stat(full)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != int64(len(data)) {
		t.Errorf("file size = %d, want %d", info.Size(), len(data))
	}
}

func TestReadArtBytes_PathTraversal(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()

	_, err := s.ReadArtBytes("../etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal")
	}

	_, err = s.ReadArtBytes(".art/../../etc/passwd")
	if err == nil {
		t.Error("expected error for sneaky path traversal")
	}
}

func TestReadArtBytes_Nonexistent(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()

	_, err := s.ReadArtBytes(".art/nonexistent.jpg")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected ErrNotExist, got %v", err)
	}
}

func TestReadArtBytes_OutsideArtDir(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()

	_, err := s.ReadArtBytes("some-other-dir/file.jpg")
	if err == nil {
		t.Error("expected error for path outside .art")
	}
}

func TestExtractArtwork_NonExistentHash(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	_, _, err := s.ExtractArtwork(context.Background(), metainfo.Hash{}, 0)
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestExtractSubtitle_NonExistentHash(t *testing.T) {
	s := NewForTesting()
	_, err := s.ExtractSubtitle(context.Background(), metainfo.Hash{}, 0, 0)
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestExtractThumbnail_NonExistentHash(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	_, _, err := s.ExtractThumbnail(context.Background(), metainfo.Hash{}, 0, 0)
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}
