package streamer

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/tracker"
	"github.com/anacrolix/torrent/tracker/udp"
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

// scrapeHTTPClient is reused across HTTP scrapes; per-call timeout comes from the
// context. No redirect-following surprises — a scrape is a single GET.
var scrapeHTTPClient = &http.Client{}

// scrapeOneTracker scrapes a single tracker. HTTP(S) goes through our own client
// (httpScrapeTracker) instead of anacrolix's — the latter log.Printf's the full
// scrape URL, which leaks a private tracker's passkey into our logs. UDP uses
// anacrolix (its scrape carries no passkey in the URL).
func scrapeOneTracker(ctx context.Context, trURL string, ih infohash.T) (seeders, leechers int, ok bool) {
	u, err := url.Parse(trURL)
	if err != nil {
		return 0, 0, false
	}
	cctx, cancel := context.WithTimeout(ctx, scrapePerTracker)
	defer cancel()

	switch u.Scheme {
	case "http", "https":
		return httpScrapeTracker(cctx, u, ih)
	case "udp", "udp4", "udp6":
		return udpScrapeTracker(cctx, trURL, ih)
	default:
		return 0, 0, false
	}
}

// httpScrapeTracker performs a BEP 48 HTTP scrape ourselves: rewrite the trailing
// announce segment to "scrape" (preserving any passkey in the path/query) and GET
// it, decoding the bencoded {files: {<ih>: {complete,incomplete,downloaded}}}.
func httpScrapeTracker(ctx context.Context, announce *url.URL, ih infohash.T) (seeders, leechers int, ok bool) {
	su := announce.JoinPath("..", "scrape") // .../announce → .../scrape (BEP 48)
	q := su.Query()
	q.Add("info_hash", string(ih[:])) // raw 20 bytes, percent-encoded by Encode
	su.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, su.String(), nil)
	if err != nil {
		return 0, 0, false
	}
	resp, err := scrapeHTTPClient.Do(req)
	if err != nil {
		return 0, 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, false
	}
	var decoded struct {
		Files map[string]udp.ScrapeInfohashResult `bencode:"files"`
	}
	if err := bencode.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&decoded); err != nil {
		return 0, 0, false
	}
	return nonEmptyScrape(decoded.Files[ih.AsString()])
}

// udpScrapeTracker scrapes a UDP tracker via anacrolix (handles the connect+scrape
// handshake). UDP scrape URLs don't carry a passkey, so the log leak isn't a concern.
func udpScrapeTracker(ctx context.Context, trURL string, ih infohash.T) (seeders, leechers int, ok bool) {
	cl, err := tracker.NewClient(trURL, tracker.NewClientOpts{})
	if err != nil {
		return 0, 0, false
	}
	defer cl.Close()
	resp, err := cl.Scrape(ctx, []infohash.T{ih})
	if err != nil || len(resp) == 0 {
		return 0, 0, false
	}
	return nonEmptyScrape(resp[0])
}

// nonEmptyScrape unwraps a scrape row, treating an all-zero row as "tracker
// doesn't know this torrent" (ok=false) so it can't mask a positive count from
// another tracker in the max().
func nonEmptyScrape(r udp.ScrapeInfohashResult) (seeders, leechers int, ok bool) {
	if r.Seeders == 0 && r.Leechers == 0 && r.Completed == 0 {
		return 0, 0, false
	}
	return int(r.Seeders), int(r.Leechers), true
}

// trackersFromMetainfo flattens a .torrent's announce tiers — including the
// passkey-bearing URLs of private trackers (amigos-share) that ship no magnet.
func trackersFromMetainfo(mi *metainfo.MetaInfo) []string {
	if mi == nil {
		return nil
	}
	out := []string{}
	for _, tier := range mi.UpvertedAnnounceList() {
		out = append(out, tier...)
	}
	return out
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
