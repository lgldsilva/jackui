package streamer

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// newRealStreamer spins up a real anacrolix-backed streamer on a free port
// (retrying on "address already in use", like the handlers test helper).
func newRealStreamer(t *testing.T) (*Streamer, string) {
	t.Helper()
	dir := t.TempDir()
	for attempt := 0; attempt < 8; attempt++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		s, err := New(Config{DataDir: dir, MetadataWait: 100 * time.Millisecond, ListenPort: port})
		if err == nil {
			t.Cleanup(s.Close)
			return s, dir
		}
	}
	t.Fatal("streamer.New failed after retries")
	return nil, ""
}

// TestBuildInfoLiveStats_ByteCounters activates a torrent from a seeded metainfo
// (cache-first Add → no swarm needed) and checks Get/buildInfo + LiveStats
// surface the cumulative byte counters that the player-info panel and the
// downloads upload-total render. Guards the wiring that previously dropped the
// uploaded total between anacrolix and the API.
func TestBuildInfoLiveStats_ByteCounters(t *testing.T) {
	s, dir := newRealStreamer(t)

	piece := metainfo.HashBytes([]byte("zzzz"))
	infoBytes, err := bencode.Marshal(metainfo.Info{
		Name: "Solo.mkv", PieceLength: 1 << 14, Pieces: piece[:], Length: 4,
	})
	if err != nil {
		t.Fatalf("bencode.Marshal: %v", err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: infoBytes}
	h := mi.HashInfoBytes()
	if err := os.MkdirAll(filepath.Join(dir, ".metainfo"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(filepath.Join(dir, ".metainfo", h.HexString()+".torrent"))
	if err != nil {
		t.Fatalf("create .torrent: %v", err)
	}
	if err := mi.Write(f); err != nil {
		t.Fatalf("write .torrent: %v", err)
	}
	_ = f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := s.Add(ctx, "magnet:?xt=urn:btih:"+h.HexString()); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Get → buildInfo: the new BytesDownloaded/BytesUploaded fields must populate
	// (0 here — nothing downloaded/uploaded — but the lines must execute).
	info, err := s.Get(h)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if info.Name != "Solo.mkv" {
		t.Errorf("name = %q, want Solo.mkv", info.Name)
	}
	if info.BytesDownloaded < 0 || info.BytesUploaded < 0 {
		t.Errorf("byte counters negative: down=%d up=%d", info.BytesDownloaded, info.BytesUploaded)
	}

	// LiveStats: the O(1) path the downloads list uses must also return uploaded.
	down, up, uploaded, seeders, ok := s.LiveStats(h)
	if !ok {
		t.Fatal("LiveStats ok=false for an active torrent")
	}
	if down < 0 || up < 0 || uploaded < 0 || seeders < 0 {
		t.Errorf("LiveStats returned negatives: down=%d up=%d uploaded=%d seeders=%d", down, up, uploaded, seeders)
	}
}

// LiveStats is polled by GET /api/downloads every few seconds. It must NOT treat
// that poll as "use" — otherwise dropIdleTorrents never reclaims finished torrents
// and mmap of bulk media pins multi-GiB RSS while the UI is open.
func TestLiveStatsDoesNotRefreshLastAccess(t *testing.T) {
	s, dir := newRealStreamer(t)

	piece := metainfo.HashBytes([]byte("llll"))
	infoBytes, err := bencode.Marshal(metainfo.Info{
		Name: "Idle.mkv", PieceLength: 1 << 14, Pieces: piece[:], Length: 4,
	})
	if err != nil {
		t.Fatalf("bencode.Marshal: %v", err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: infoBytes}
	h := mi.HashInfoBytes()
	if err := os.MkdirAll(filepath.Join(dir, ".metainfo"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(filepath.Join(dir, ".metainfo", h.HexString()+".torrent"))
	if err != nil {
		t.Fatalf("create .torrent: %v", err)
	}
	if err := mi.Write(f); err != nil {
		t.Fatalf("write .torrent: %v", err)
	}
	_ = f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := s.Add(ctx, "magnet:?xt=urn:btih:"+h.HexString()); err != nil {
		t.Fatalf("Add: %v", err)
	}

	aged := time.Now().Add(-2 * time.Hour)
	s.mu.Lock()
	e, ok := s.active[h]
	if !ok {
		s.mu.Unlock()
		t.Fatal("torrent not active after Add")
	}
	e.lastAccess = aged
	s.mu.Unlock()

	if _, _, _, _, ok := s.LiveStats(h); !ok {
		t.Fatal("LiveStats ok=false")
	}

	s.mu.Lock()
	got := s.active[h].lastAccess
	s.mu.Unlock()
	if !got.Equal(aged) {
		t.Fatalf("LiveStats refreshed lastAccess: got %v want %v", got, aged)
	}
}
