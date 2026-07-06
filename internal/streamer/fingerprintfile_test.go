package streamer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/contentid"
)

func TestFingerprintFile_NotActive(t *testing.T) {
	s := NewForTesting()
	if _, err := s.FingerprintFile(metainfo.Hash{}, 0); err == nil {
		t.Fatal("expected error for a torrent that isn't active")
	}
}

func TestFingerprintFile_OutOfRange(t *testing.T) {
	s, hash := activeMultiPiece(t, make([]byte, 1<<14), 1<<14)
	if _, err := s.FingerprintFile(hash, 99); err == nil {
		t.Fatal("expected error for an out-of-range file index")
	}
}

func TestFingerprintFile_MatchesOnDiskFingerprint(t *testing.T) {
	const pieceLen = 1 << 14
	data := make([]byte, 4*pieceLen)
	for i := range data {
		data[i] = byte((i*11 + 3) % 251)
	}
	dir := t.TempDir()
	s, err := newTestStreamer(t, Config{DataDir: dir})
	if err != nil {
		t.Fatalf("newTestStreamer: %v", err)
	}
	t.Cleanup(s.Close)
	tor, _, err := s.client.AddTorrentSpec(buildMultiPieceSpec(t, data, pieceLen))
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	hash := tor.InfoHash()
	// Put the content where the file backend expects it, then reconcile so reads
	// succeed with no peer (FingerprintFile reads the head/tail through the reader).
	if err := os.WriteFile(filepath.Join(dir, "multi.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.active[hash] = &entry{t: tor, lastAccess: time.Now()}
	s.mu.Unlock()
	if err := s.VerifyFile(hash, 0); err != nil {
		t.Fatalf("VerifyFile: %v", err)
	}

	fp, err := s.FingerprintFile(hash, 0)
	if err != nil {
		t.Fatalf("FingerprintFile: %v", err)
	}
	ref := filepath.Join(dir, "ref.bin")
	if err := os.WriteFile(ref, data, 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := contentid.Fingerprint(ref, int64(len(data)))
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if fp == "" || fp != want {
		t.Fatalf("torrent fingerprint %q != on-disk %q", fp, want)
	}
}
