package streamer

import (
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

func ahNewStreamer(t *testing.T) (*Streamer, *MetadataCache) {
	t.Helper()
	s := NewForTesting()
	mc, err := NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mc.Close() })
	s.SetMetadataCache(mc)
	return s, mc
}

// activeHealth: live count used as-is when there's no higher scrape; trackers nil
// keeps maybeScrapeActive a no-op (no goroutine in this test).
func TestActiveHealth_LiveOnly(t *testing.T) {
	s, _ := ahNewStreamer(t)
	h := s.activeHealth(scrapeTestHash(), 3, 5, nil)
	if h.Seeders != 3 || h.Peers != 5 || !h.Available {
		t.Fatalf("got %+v, want seeders=3 peers=5 available", h)
	}
}

// activeHealth: a cached scrape larger than the live count wins (the badge shows
// the real swarm size, not the 0 we happen to be connected to).
func TestActiveHealth_ScrapeBeatsLive(t *testing.T) {
	s, mc := ahNewStreamer(t)
	hash := scrapeTestHash()
	if err := mc.SetHealth(hash.HexString(), 50, 2); err != nil {
		t.Fatal(err)
	}
	h := s.activeHealth(hash, 0, 1, nil) // nil trackers → no scrape kicked
	if h.Seeders != 50 {
		t.Fatalf("got seeders=%d, want 50 (cached scrape)", h.Seeders)
	}
}

// maybeScrapeActive: no trackers / a fresh cache → no-op (nothing persisted).
func TestMaybeScrapeActive_NoOps(t *testing.T) {
	s, mc := ahNewStreamer(t)
	hash := scrapeTestHash()
	s.maybeScrapeActive(hash, nil, nil) // no trackers
	srv := fakeScrapeTracker(t, hash, 99, 9)
	s.maybeScrapeActive(hash, []string{srv.URL + "/announce"}, &CachedHealth{Seeders: 1, CheckedAt: time.Now()})
	// Neither call should have scraped/persisted anything.
	if h := mc.GetHealth(hash.HexString()); h != nil {
		t.Fatalf("expected no persisted health, got %+v", h)
	}
}

// maybeScrapeActive: stale/absent cache + trackers → scrapes in the background
// and persists the real numbers.
func TestMaybeScrapeActive_Scrapes(t *testing.T) {
	s, mc := ahNewStreamer(t)
	hash := scrapeTestHash()
	srv := fakeScrapeTracker(t, hash, 33, 4)
	s.maybeScrapeActive(hash, []string{srv.URL + "/announce"}, nil)
	// The scrape runs in a goroutine; poll the cache briefly for the result.
	var got *CachedHealth
	for i := 0; i < 100; i++ {
		if h := mc.GetHealth(hash.HexString()); h != nil && h.Seeders == 33 {
			got = h
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got == nil || got.Seeders != 33 || got.Peers != 4 {
		t.Fatalf("scrape did not persist (got %+v)", got)
	}
}
