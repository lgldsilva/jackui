package streamer

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/tracker"
	"github.com/anacrolix/torrent/types/infohash"
)

// Tracker scrape (BEP 48) gives the REAL swarm size the tracker publishes
// (complete/incomplete), instead of how many peers we managed to connect to in a
// short window. It's the number the tracker's own site shows — and it doesn't
// depend on Jackett's per-indexer parsing, which over/under-reports.

const (
	// scrapePerTracker caps a single tracker's scrape. Dead/slow trackers must
	// not stall the whole probe.
	scrapePerTracker = 5 * time.Second
	// scrapeBudget bounds the whole multi-tracker scrape (they run concurrently).
	scrapeBudget = 6 * time.Second
	// scrapeMaxTrackers caps how many trackers we hit per torrent — public
	// torrents can list dozens; the first handful is plenty for a max().
	scrapeMaxTrackers = 8
)

// scrapeSwarm asks the trackers for the swarm size and returns the MAX
// complete/incomplete seen. The same swarm is announced to every tracker, so the
// max approximates the true count (summing would double-count shared peers).
// ok is false when no tracker answered (caller falls back to a live probe).
func scrapeSwarm(ctx context.Context, hash metainfo.Hash, trackers []string) (seeders, leechers int, ok bool) {
	uniq := dedupeScrapeTrackers(trackers)
	if len(uniq) > scrapeMaxTrackers {
		uniq = uniq[:scrapeMaxTrackers]
	}
	if len(uniq) == 0 {
		return 0, 0, false
	}
	ih := infohash.T(hash)

	type res struct {
		seeders, leechers int
		ok                bool
	}
	out := make(chan res, len(uniq))
	var wg sync.WaitGroup
	for _, tr := range uniq {
		wg.Add(1)
		go func(trURL string) {
			defer wg.Done()
			s, l, found := scrapeOneTracker(ctx, trURL, ih)
			out <- res{s, l, found}
		}(tr)
	}
	go func() { wg.Wait(); close(out) }()

	for r := range out {
		if !r.ok {
			continue
		}
		ok = true
		if r.seeders > seeders {
			seeders = r.seeders
		}
		if r.leechers > leechers {
			leechers = r.leechers
		}
	}
	return seeders, leechers, ok
}

// scrapeOneTracker scrapes a single tracker URL. anacrolix's HTTP client rewrites
// .../announce → .../scrape (BEP 48) and preserves the query/path passkey, so
// private trackers (jackui) work as long as the announce URL carries it.
func scrapeOneTracker(ctx context.Context, trURL string, ih infohash.T) (seeders, leechers int, ok bool) {
	cl, err := tracker.NewClient(trURL, tracker.NewClientOpts{})
	if err != nil {
		return 0, 0, false
	}
	defer cl.Close()

	cctx, cancel := context.WithTimeout(ctx, scrapePerTracker)
	defer cancel()
	resp, err := cl.Scrape(cctx, []infohash.T{ih})
	if err != nil || len(resp) == 0 {
		return 0, 0, false
	}
	r := resp[0]
	// A tracker that doesn't know the torrent returns a zeroed row; treat the
	// all-zero case as "no data" so it doesn't mask a positive count elsewhere.
	if r.Seeders == 0 && r.Leechers == 0 && r.Completed == 0 {
		return 0, 0, false
	}
	return int(r.Seeders), int(r.Leechers), true
}

// dedupeScrapeTrackers normalizes, dedupes and drops schemes we can't scrape
// (wss/ws and friends — only http(s)/udp support BEP 48 here).
func dedupeScrapeTrackers(trackers []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(trackers))
	for _, t := range trackers {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		u, err := url.Parse(t)
		if err != nil {
			continue
		}
		switch u.Scheme {
		case "http", "https", "udp", "udp4", "udp6":
		default:
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// trackersFromMagnet extracts the tr= announce URLs from a magnet link.
func trackersFromMagnet(magnet string) []string {
	if magnet == "" {
		return nil
	}
	q := magnet
	if i := strings.Index(magnet, "?"); i >= 0 {
		q = magnet[i+1:]
	}
	out := []string{}
	for _, p := range strings.Split(q, "&") {
		if !strings.HasPrefix(p, "tr=") {
			continue
		}
		if v, err := url.QueryUnescape(p[len("tr="):]); err == nil && v != "" {
			out = append(out, v)
		}
	}
	return out
}
