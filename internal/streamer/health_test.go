package streamer

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

func TestActiveEntry_Empty(t *testing.T) {
	s := NewForTesting()
	if e := s.activeEntry(metainfo.Hash{}); e != nil {
		t.Fatal("expected nil entry for empty streamer")
	}
}

func TestHealthSnapshot_NoCacheNoEntry(t *testing.T) {
	s := NewForTesting()
	health, active := s.HealthSnapshot(metainfo.Hash{})
	if health != nil {
		t.Fatal("expected nil health")
	}
	if active {
		t.Fatal("expected not active")
	}
}

func TestHealthSnapshot_WithCacheNoEntry(t *testing.T) {
	s := NewForTesting()
	c, err := NewMetadataCache(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	defer c.Close()
	s.SetMetadataCache(c)

	health, active := s.HealthSnapshot(metainfo.Hash{0x01})
	if health != nil {
		t.Fatal("expected nil health with empty cache")
	}
	if active {
		t.Fatal("expected not active")
	}
}

func TestHealthSnapshot_WithCacheReturnsPersisted(t *testing.T) {
	s := NewForTesting()
	c := newTestCache(t)
	s.SetMetadataCache(c)

	_ = c.SetHealth("0101010101010101010101010101010101010101", 5, 10)

	var h metainfo.Hash
	if err := h.FromHexString("0101010101010101010101010101010101010101"); err != nil {
		t.Fatalf("FromHexString: %v", err)
	}
	health, active := s.HealthSnapshot(h)
	if health == nil {
		t.Fatal("expected health from cache")
	}
	if health.Seeders != 5 || health.Peers != 10 {
		t.Errorf("got seeders=%d peers=%d, want 5/10", health.Seeders, health.Peers)
	}
	if active {
		t.Fatal("expected not active (cache-only)")
	}
}

func TestProbeHealthAsync_EmptyMagnetNoop(t *testing.T) {
	s := NewForTesting()
	s.ProbeHealthAsync(metainfo.Hash{}, "")
}

func TestProbeHealthAsync_Dedup(t *testing.T) {
	s := NewForTesting()
	h := metainfo.Hash{0x01}

	healthInflight = sync.Map{}
	defer func() { healthInflight = sync.Map{} }()

	healthInflight.Store(h, true)
	magnet := "magnet:?xt=urn:btih:0101010101010101010101010101010101010101"
	s.ProbeHealthAsync(h, magnet)
	// healthInflight still has h (not deleted since no goroutine ran)
	if _, loaded := healthInflight.Load(h); !loaded {
		t.Error("expected hash to remain in healthInflight")
	}
}
