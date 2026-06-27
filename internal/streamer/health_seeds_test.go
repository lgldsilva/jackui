package streamer

import (
	"testing"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

// seedersNotBelowScrape must never let a live ConnectedSeeders count drop below
// the last tracker scrape — that's the real swarm size.
func TestSeedersNotBelowScrape(t *testing.T) {
	s := NewForTesting()
	mc, err := NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mc.Close() })
	s.SetMetadataCache(mc)
	hash := scrapeTestHash()

	// No cached scrape → returns the live count unchanged.
	if got := s.seedersNotBelowScrape(hash, 5); got != 5 {
		t.Fatalf("no cache: got %d, want 5", got)
	}
	// Scrape says 50, live (connected) is 0 → clamp up to 50.
	if err := mc.SetHealth(hash.HexString(), 50, 3); err != nil {
		t.Fatal(err)
	}
	if got := s.seedersNotBelowScrape(hash, 0); got != 50 {
		t.Fatalf("scrape>live: got %d, want 50", got)
	}
	// Live exceeds the cached scrape → keep the (higher) live count.
	if got := s.seedersNotBelowScrape(hash, 80); got != 80 {
		t.Fatalf("live>scrape: got %d, want 80", got)
	}
}
