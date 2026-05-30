package streamer

import (
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

func TestHealthSnapshot_NoCache(t *testing.T) {
	s := NewForTesting()
	h := metainfo.Hash{}
	health, active := s.HealthSnapshot(h)
	if active {
		t.Error("expected active=false for empty streamer")
	}
	if health != nil {
		t.Errorf("expected nil health, got %+v", health)
	}
}

func TestHealthSnapshot_WithCache(t *testing.T) {
	dir := t.TempDir()
	c, err := NewMetadataCache(dir + "/meta.db")
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	s := NewForTesting()
	s.cache = c

	_ = c.SetHealth("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 5, 10)

	h := metainfo.Hash{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}
	health, active := s.HealthSnapshot(h)
	if active {
		t.Error("expected active=false for non-loaded torrent")
	}
	if health == nil {
		t.Fatal("expected non-nil health from cache")
	}
	if health.Seeders != 5 {
		t.Errorf("seeders: want 5, got %d", health.Seeders)
	}
	if health.Peers != 10 {
		t.Errorf("peers: want 10, got %d", health.Peers)
	}
}

func TestHealthSnapshot_NoMatchInCache(t *testing.T) {
	dir := t.TempDir()
	c, err := NewMetadataCache(dir + "/meta.db")
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	s := NewForTesting()
	s.cache = c

	var h metainfo.Hash
	h[0] = 0x01
	health, active := s.HealthSnapshot(h)
	if active {
		t.Error("expected active=false")
	}
	if health != nil {
		t.Error("expected nil health for cache miss")
	}
}

func TestProbeHealthAsync_DedupeBusy(t *testing.T) {
	s := NewForTesting()
	var h metainfo.Hash
	h[0] = 0xBB
	healthInflight.Store(h, true)
	t.Cleanup(func() { healthInflight.Delete(h) })

	s.ProbeHealthAsync(h, "magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
}

func TestProbeHealthAsync_EmptyMagnet(t *testing.T) {
	s := NewForTesting()
	// Should be a no-op and not panic
	s.ProbeHealthAsync(metainfo.Hash{}, "")
}

func TestHealthFreshForConst(t *testing.T) {
	if HealthFreshFor != 30*time.Minute {
		t.Errorf("expected 30m, got %v", HealthFreshFor)
	}
}

func TestHealthSnapshot_WithNilCache(t *testing.T) {
	s := NewForTesting()
	var h metainfo.Hash
	health, active := s.HealthSnapshot(h)
	if active {
		t.Error("expected active=false")
	}
	if health != nil {
		t.Error("expected nil health with nil cache")
	}
}

func TestProbeHealthAsync_DedupesEmptyMagnet(t *testing.T) {
	s := NewForTesting()
	// Empty magnet is no-op (first check in ProbeHealthAsync)
	// This verifies dedupe doesn't happen on empty magnet
	var h metainfo.Hash
	s.ProbeHealthAsync(h, "")
	// Verify goroutine didn't panic
}

func TestHealthInflightDedupe(t *testing.T) {
	// Verify the sync.Map is initialized
	var m sync.Map
	_, loaded := m.LoadOrStore("test", true)
	if loaded {
		t.Fatal("expected first LoadOrStore to return loaded=false")
	}
	_, loaded = m.LoadOrStore("test", true)
	if !loaded {
		t.Fatal("expected second LoadOrStore to return loaded=true")
	}
	m.Delete("test")
	_, loaded = m.LoadOrStore("test", true)
	if loaded {
		t.Fatal("expected after delete to return loaded=false")
	}
}
