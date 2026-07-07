package streamer

import (
	"fmt"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

func TestVerifyTorrent_NotActive(t *testing.T) {
	s := NewForTesting()
	if err := s.VerifyTorrent(metainfo.Hash{}); err == nil {
		t.Error("expected error for a torrent that isn't active")
	}
}

func TestRecheckAllFiles_NotActive(t *testing.T) {
	s := NewForTesting()
	if err := s.RecheckAllFiles(metainfo.Hash{}); err == nil {
		t.Error("expected error for a torrent that isn't active")
	}
}

// VerifyTorrent over an active info-complete torrent must claim every file's
// verify key (the per-process dedupe VerifyFile uses), proving each file got a
// reconciliation pass.
func TestVerifyTorrent_ClaimsEveryFile(t *testing.T) {
	dir := t.TempDir()
	s, err := newTestStreamer(t, Config{DataDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	spec := str3TorrentSpec(t)
	tor, _, err := s.client.AddTorrentSpec(spec)
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	hash := tor.InfoHash()
	s.mu.Lock()
	s.active[hash] = &entry{t: tor, lastAccess: time.Now()}
	s.mu.Unlock()

	if err := s.VerifyTorrent(hash); err != nil {
		t.Fatalf("VerifyTorrent: %v", err)
	}
	s.verifiedMu.Lock()
	defer s.verifiedMu.Unlock()
	for i := range tor.Files() {
		key := fmt.Sprintf("%s-%d", hash.HexString(), i)
		if !s.verifiedFiles[key] {
			t.Errorf("file %d was not verified (key %q missing)", i, key)
		}
	}
}

// RecheckAllFiles re-hashes every file sequentially. After it returns, every
// file must hold a verify claim again (recheckFilePieces marks them as it completes).
func TestRecheckAllFiles_RehashesEveryFile(t *testing.T) {
	dir := t.TempDir()
	s, err := newTestStreamer(t, Config{DataDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	spec := str3TorrentSpec(t)
	tor, _, err := s.client.AddTorrentSpec(spec)
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	hash := tor.InfoHash()
	s.mu.Lock()
	s.active[hash] = &entry{t: tor, lastAccess: time.Now()}
	s.mu.Unlock()

	if err := s.RecheckAllFiles(hash); err != nil {
		t.Fatalf("RecheckAllFiles: %v", err)
	}
	s.verifiedMu.Lock()
	defer s.verifiedMu.Unlock()
	for i := range tor.Files() {
		key := fmt.Sprintf("%s-%d", hash.HexString(), i)
		if !s.verifiedFiles[key] {
			t.Errorf("file %d was not rechecked (key %q missing)", i, key)
		}
	}
}
