package streamer

import (
	"testing"

	"github.com/anacrolix/torrent"
)

// ListenPort exposes the BitTorrent peer port (used by the RPC session-get).
func TestListenPortGetter(t *testing.T) {
	s := &Streamer{cfg: Config{ListenPort: 51470}}
	if got := s.ListenPort(); got != 51470 {
		t.Errorf("ListenPort() = %d, want 51470", got)
	}
}

func TestStreamReadaheadDefaultAndSetter(t *testing.T) {
	s := NewForTesting()
	if got := s.streamReadahead(); got != streamReadaheadDefault {
		t.Errorf("default readahead = %d, want %d", got, streamReadaheadDefault)
	}
	s.SetStreamReadahead(8)
	if got := s.streamReadahead(); got != 8<<20 {
		t.Errorf("after Set(8) = %d, want %d", got, 8<<20)
	}
	// 0/negative reverts to the default.
	s.SetStreamReadahead(0)
	if got := s.streamReadahead(); got != streamReadaheadDefault {
		t.Errorf("after Set(0) = %d, want default %d", got, streamReadaheadDefault)
	}
	s.SetStreamReadahead(-5)
	if got := s.streamReadahead(); got != streamReadaheadDefault {
		t.Errorf("after Set(-5) = %d, want default", got)
	}
}

func TestApplyPeerTuning(t *testing.T) {
	// >0 overrides; 0 preserves the library default.
	base := torrent.NewDefaultClientConfig()
	defConns := base.EstablishedConnsPerTorrent
	defHashers := base.PieceHashersPerTorrent

	tcfg := torrent.NewDefaultClientConfig()
	applyPeerTuning(tcfg, Config{MaxConnsPerTorrent: 120, HalfOpenConns: 40, PeersHighWater: 900, PieceHashers: 6})
	if tcfg.EstablishedConnsPerTorrent != 120 {
		t.Errorf("conns = %d, want 120", tcfg.EstablishedConnsPerTorrent)
	}
	if tcfg.HalfOpenConnsPerTorrent != 40 {
		t.Errorf("halfOpen = %d, want 40", tcfg.HalfOpenConnsPerTorrent)
	}
	if tcfg.TorrentPeersHighWater != 900 {
		t.Errorf("peersHighWater = %d, want 900", tcfg.TorrentPeersHighWater)
	}
	if tcfg.PieceHashersPerTorrent != 6 {
		t.Errorf("pieceHashers = %d, want 6", tcfg.PieceHashersPerTorrent)
	}
	_ = defHashers

	// All-zero config changes nothing.
	tcfg2 := torrent.NewDefaultClientConfig()
	applyPeerTuning(tcfg2, Config{})
	if tcfg2.EstablishedConnsPerTorrent != defConns {
		t.Errorf("conns changed with zero config: %d, want %d", tcfg2.EstablishedConnsPerTorrent, defConns)
	}
}

// New with the mmap backend creates the client, registers storageImpl and Close
// closes it without panic. Validates the configurable storage path end-to-end.
func TestNewWithMmapStorageClosesCleanly(t *testing.T) {
	dir := t.TempDir()
	s, err := newTestStreamer(t, Config{DataDir: dir, StorageBackend: "mmap"})
	if err != nil {
		t.Fatalf("New(mmap): %v", err)
	}
	if s.storageImpl == nil {
		t.Fatal("mmap backend should have set storageImpl non-nil")
	}
	s.Close() // must close the mmap without panic
}

// New with the file backend (default) does NOT set storageImpl — it uses the
// client-managed FileStorage.
func TestNewWithFileStorageHasNoExplicitImpl(t *testing.T) {
	dir := t.TempDir()
	s, err := newTestStreamer(t, Config{DataDir: dir, StorageBackend: "file"})
	if err != nil {
		t.Fatalf("New(file): %v", err)
	}
	defer s.Close()
	if s.storageImpl != nil {
		t.Error("file backend should not set an explicit storageImpl")
	}
}

// Readahead configured in New is reflected by streamReadahead.
func TestNewAppliesConfiguredReadahead(t *testing.T) {
	dir := t.TempDir()
	s, err := newTestStreamer(t, Config{DataDir: dir, Readahead: 16 << 20})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	// Via the exported accessor (same path other packages use).
	if got := s.StreamReadaheadForTesting(); got != 16<<20 {
		t.Errorf("readahead = %d, want %d", got, 16<<20)
	}
}
