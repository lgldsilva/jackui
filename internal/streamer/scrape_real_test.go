package streamer

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// Manual/empirical: hits REAL public trackers. Gated by JACKUI_SCRAPE_REAL=1 so
// it never runs in CI. Big Buck Bunny — a perennially well-seeded public torrent.
func TestScrapeReal_PublicTrackers(t *testing.T) {
	if os.Getenv("JACKUI_SCRAPE_REAL") != "1" {
		t.Skip("set JACKUI_SCRAPE_REAL=1 to hit real trackers")
	}
	var hash metainfo.Hash
	if err := hash.FromHexString("dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c"); err != nil {
		t.Fatal(err)
	}
	trackers := []string{
		"udp://tracker.opentrackr.org:1337/announce",
		"udp://open.tracker.cl:1337/announce",
		"udp://tracker.openbittorrent.com:6969/announce",
		"https://tracker.gbitt.info:443/announce",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, l, ok := scrapeSwarm(ctx, hash, trackers)
	t.Logf("scrape result: seeders=%d leechers=%d ok=%v", s, l, ok)
	if !ok {
		t.Fatal("no tracker answered (network blocked? UDP filtered?)")
	}
	if s == 0 {
		t.Fatal("expected a non-zero seeder count for Big Buck Bunny")
	}
}
