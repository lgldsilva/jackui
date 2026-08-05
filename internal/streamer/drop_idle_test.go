package streamer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// dropIdleTorrents removes a torrent idle beyond IdleTimeout when it is neither
// download-protected nor seed-tracked. Covers the previously untested eviction
// loop in streamer_gc.go.
func TestStreamer_DropIdleTorrents_EvictsIdle(t *testing.T) {
	s := NewForTesting()
	s.cfg.IdleTimeout = time.Hour

	// An idle, unprotected, non-seeding torrent must be dropped.
	h := metainfo.Hash{0x01}
	stale := time.Now().Add(-2 * time.Hour)
	// entry requires a real *torrent.Torrent; a nil t would panic on t.Name().
	// We create a minimal fake via a torrent spec in a throwaway client.
	tor := newTestTorrent(t)
	s.mu.Lock()
	s.active[h] = &entry{t: tor, lastAccess: stale}
	s.mu.Unlock()

	dropped := s.dropIdleTorrents(time.Now())

	if len(dropped) != 1 || dropped[0] != h {
		t.Fatalf("dropped = %v, want [%v]", dropped, h)
	}
	s.mu.Lock()
	_, stillActive := s.active[h]
	s.mu.Unlock()
	if stillActive {
		t.Fatal("idle torrent should have been removed from active")
	}
}

func TestStreamer_DropIdleTorrents_KeepsRecentAndProtected(t *testing.T) {
	s := NewForTesting()
	s.cfg.IdleTimeout = time.Hour

	recentH := metainfo.Hash{0x02}
	protectedH := metainfo.Hash{0x03}
	tor := newTestTorrent(t)
	name := tor.Name()

	s.mu.Lock()
	s.active[recentH] = &entry{t: tor, lastAccess: time.Now()}
	s.active[protectedH] = &entry{t: tor, lastAccess: time.Now().Add(-2 * time.Hour)}
	s.mu.Unlock()
	s.RegisterDownload(name)

	dropped := s.dropIdleTorrents(time.Now())

	if len(dropped) != 0 {
		t.Fatalf("dropped = %v, want none", dropped)
	}
	s.mu.Lock()
	_, recentStill := s.active[recentH]
	_, protectedStill := s.active[protectedH]
	s.mu.Unlock()
	if !recentStill {
		t.Fatal("recent torrent must be kept")
	}
	if !protectedStill {
		t.Fatal("download-protected torrent must be kept")
	}
}

func TestStreamer_DropIdleTorrents_StampsMtime(t *testing.T) {
	dir := t.TempDir()
	s := NewForTesting()
	s.cfg.DataDir = dir
	s.cfg.IdleTimeout = time.Hour

	h := metainfo.Hash{0x04}
	name := "idle-entry"
	// The stamp path requires the on-disk entry to exist.
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	ancient := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(p, ancient, ancient); err != nil {
		t.Fatal(err)
	}

	lastAccess := time.Now().Add(-2 * time.Hour)
	tor := newTestTorrentNamed(t, name)
	s.mu.Lock()
	s.active[h] = &entry{t: tor, lastAccess: lastAccess}
	s.mu.Unlock()

	_ = s.dropIdleTorrents(time.Now())

	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("entry dir vanished: %v", err)
	}
	if info.ModTime().Before(lastAccess.Add(-time.Minute)) {
		t.Errorf("mtime not stamped with lastAccess: mtime=%s lastAccess=%s",
			info.ModTime().Format(time.RFC3339), lastAccess.Format(time.RFC3339))
	}
}

// SetVerifyConcurrency / VerifyConcurrency round-trip on a live limiter and
// tolerate nil receivers (guard clauses introduced for S3776-friendly testing).
func TestStreamer_VerifyConcurrency_RoundTrip(t *testing.T) {
	s := NewForTesting()
	s.SetVerifyConcurrency(3)
	if got := s.VerifyConcurrency(); got != 3 {
		t.Fatalf("VerifyConcurrency = %d, want 3", got)
	}
	s.SetVerifyConcurrency(0) // clamps to 1
	if got := s.VerifyConcurrency(); got != 1 {
		t.Fatalf("VerifyConcurrency after 0 = %d, want 1", got)
	}
	var nilStreamer *Streamer
	nilStreamer.SetVerifyConcurrency(9) // must not panic
	if got := nilStreamer.VerifyConcurrency(); got != 0 {
		t.Fatalf("nil VerifyConcurrency = %d, want 0", got)
	}
}

// newTestTorrent builds a minimal info-complete torrent in a throwaway client.
func newTestTorrent(t *testing.T) *torrent.Torrent {
	t.Helper()
	return newTestTorrentNamed(t, "test-torrent")
}

// newTestTorrentNamed builds a minimal info-complete torrent with a specific
// display name (used to verify on-disk entry stamping).
func newTestTorrentNamed(t *testing.T, name string) *torrent.Torrent {
	t.Helper()
	const piece = 1 << 14
	data := bytes.Repeat([]byte("z"), piece)
	pieceHash := metainfo.HashBytes(data)
	info := metainfo.Info{
		Name:        name,
		PieceLength: piece,
		Files:       []metainfo.FileInfo{{Path: []string{"file.bin"}, Length: 4}},
		Pieces:      pieceHash[:],
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("bencode.Marshal: %v", err)
	}
	spec, err := torrent.TorrentSpecFromMetaInfoErr(&metainfo.MetaInfo{InfoBytes: infoBytes})
	if err != nil {
		t.Fatalf("TorrentSpecFromMetaInfoErr: %v", err)
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = t.TempDir()
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.DisableTCP = true
	cfg.DisableUTP = true
	cfg.ListenPort = 0
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatalf("torrent.NewClient: %v", err)
	}
	t.Cleanup(func() { cl.Close() })
	tor, _, err := cl.AddTorrentSpec(spec)
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	return tor
}
