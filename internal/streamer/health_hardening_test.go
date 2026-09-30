package streamer

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

// ─── F6: probeHealth must read Stats() under s.mu ────────────────────────────

// TestProbeHealth_ActiveStatsUnderLock hammers the active-entry branch of
// probeHealth against a concurrent teardown. Before the fix the Stats() read
// sat OUTSIDE s.mu (activeEntry had already released it) — the same TOCTOU
// HealthSnapshot fixed (invariant comment at health.go:37-47): a Drop tearing
// the torrent down in between made the probe read a dead torrent and persist a
// bogus zeroed snapshot. Run under -race. The window is narrow, so absence of
// a failure here is not proof; the real guarantee is by construction (the read
// is serialized with the teardown under s.mu, mirroring HealthSnapshot).
func TestProbeHealth_ActiveStatsUnderLock(t *testing.T) {
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	spec := str3TorrentSpec(t)
	tor, _, err := s.client.AddTorrentSpec(spec)
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	hash := tor.InfoHash()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // teardown hammer: age the read guard, then Drop
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			s.mu.Lock()
			if e, ok := s.active[hash]; ok {
				e.lastAccess = time.Now().Add(-2 * activeReadGuard) // let Drop through
			}
			s.mu.Unlock()
			s.Drop(hash)
		}
	}()
	for i := 0; i < 100; i++ {
		// Best-effort revive: the teardown hammer may still be dropping the
		// previous torrent object (anacrolix refuses the re-add with "torrent
		// closed"). That is fine — the point is that probeHealth must stay
		// race-panic-free whether the entry is present or absent.
		if tor2, _, err := s.client.AddTorrentSpec(spec); err == nil {
			s.mu.Lock()
			s.active[tor2.InfoHash()] = &entry{t: tor2, lastAccess: time.Now()}
			s.mu.Unlock()
		} else {
			time.Sleep(time.Millisecond)
		}
		s.probeHealth(hash, "")
	}
	close(stop)
	wg.Wait()
}

// ─── F7: probe fallback must not zero the persisted snapshot ─────────────────

// TestProbeHealth_FallbackAddFailurePreservesSnapshot: when no tracker answers
// and the swarm-connect fallback can't even add the magnet, the probe used to
// write SetHealth(hash, 0, 0) — clobbering a good persisted scrape and
// contradicting the "leave the previous snapshot" policy three lines above.
// A failed fallback must return WITHOUT writing.
func TestProbeHealth_FallbackAddFailurePreservesSnapshot(t *testing.T) {
	c, err := NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir(), MetadataWait: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	s.cache = c

	hashHex := strings.Repeat("bb", 20)
	var hash metainfo.Hash
	if err := hash.FromHexString(hashHex); err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := c.SetHealth(hashHex, 5, 7); err != nil {
		t.Fatalf("SetHealth: %v", err)
	}

	// Tracker-less magnet (no tr=, no cached .torrent) → the scrape no-ops;
	// magnet != "" → the fallback runs; MetadataWait=100ms with no swarm →
	// Add fails. The old code then zeroed the snapshot.
	s.probeHealth(hash, "magnet:?xt=urn:btih:"+hashHex)

	h := c.GetHealth(hashHex)
	if h == nil {
		t.Fatal("health snapshot vanished after a failed probe")
	}
	if h.Seeders != 5 || h.Peers != 7 {
		t.Fatalf("persisted snapshot clobbered by failed probe: seeders=%d peers=%d, want 5/7", h.Seeders, h.Peers)
	}
}
