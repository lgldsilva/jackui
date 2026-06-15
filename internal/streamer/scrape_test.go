package streamer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/tracker/udp"
	"github.com/anacrolix/torrent/types/infohash"
)

func scrapeTestHash() metainfo.Hash {
	var h metainfo.Hash
	for i := range h {
		h[i] = byte(i + 1)
	}
	return h
}

// fakeScrapeTracker serves a BEP 48 HTTP scrape for the given hash. Responds 404
// on anything but the /scrape path so we also prove the announce→scrape rewrite.
func fakeScrapeTracker(t *testing.T, hash metainfo.Hash, complete, incomplete int32) *httptest.Server {
	t.Helper()
	ihKey := hash.AsString()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/scrape") {
			http.NotFound(w, r)
			return
		}
		resp := map[string]any{
			"files": map[string]udp.ScrapeInfohashResult{
				ihKey: {Seeders: complete, Leechers: incomplete, Completed: 1},
			},
		}
		b, err := bencode.Marshal(resp)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// scrapeSwarm returns the MAX complete/incomplete across reachable trackers.
func TestScrapeSwarm_MaxAcrossTrackers(t *testing.T) {
	hash := scrapeTestHash()
	small := fakeScrapeTracker(t, hash, 10, 3)
	big := fakeScrapeTracker(t, hash, 42, 7)

	seeders, leechers, ok := scrapeSwarm(context.Background(), hash,
		[]string{small.URL + "/announce", big.URL + "/announce"})
	if !ok || seeders != 42 || leechers != 7 {
		t.Fatalf("got s=%d l=%d ok=%v, want 42/7/true", seeders, leechers, ok)
	}
}

// No reachable/scrapeable trackers → ok=false (caller falls back to live probe).
func TestScrapeSwarm_NoTrackers(t *testing.T) {
	if _, _, ok := scrapeSwarm(context.Background(), scrapeTestHash(), nil); ok {
		t.Fatal("expected ok=false with no trackers")
	}
	// wss:// is unsupported → filtered out → ok=false.
	if _, _, ok := scrapeSwarm(context.Background(), scrapeTestHash(), []string{"wss://x/announce"}); ok {
		t.Fatal("expected ok=false for unsupported scheme")
	}
}

// An all-zero scrape row means "tracker doesn't know this torrent" → ok=false.
func TestScrapeSwarm_AllZeroIsNoData(t *testing.T) {
	hash := scrapeTestHash()
	zero := fakeScrapeTracker(t, hash, 0, 0)
	// Completed=1 in the fake keeps it non-zero; force a truly empty row:
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"files": map[string]udp.ScrapeInfohashResult{hash.AsString(): {}}}
		b, _ := bencode.Marshal(resp)
		_, _ = w.Write(b)
	}))
	t.Cleanup(empty.Close)
	if _, _, ok := scrapeSwarm(context.Background(), hash, []string{empty.URL + "/announce"}); ok {
		t.Fatal("expected ok=false for all-zero row")
	}
	// And the non-empty fake still answers (sanity that the helper itself works).
	if _, _, ok := scrapeSwarm(context.Background(), hash, []string{zero.URL + "/announce"}); !ok {
		t.Fatal("expected ok=true (Completed=1 makes the row non-empty)")
	}
}

func TestTrackersFromMagnet(t *testing.T) {
	tr1 := "http://t1.example/announce"
	tr2 := "udp://t2.example:6969/announce"
	magnet := "magnet:?xt=urn:btih:abc&dn=x&tr=" + url.QueryEscape(tr1) + "&tr=" + url.QueryEscape(tr2)
	got := trackersFromMagnet(magnet)
	if len(got) != 2 || got[0] != tr1 || got[1] != tr2 {
		t.Fatalf("got %v, want [%s %s]", got, tr1, tr2)
	}
	if trackersFromMagnet("") != nil {
		t.Fatal("empty magnet should yield nil")
	}
}

func TestDedupeScrapeTrackers(t *testing.T) {
	in := []string{
		"http://a/announce", "http://a/announce", // dup
		"udp://b:80/announce",
		"wss://c/announce", // unsupported scheme
		"   ",              // blank
		"://bad",           // unparseable-ish / no scheme
	}
	got := dedupeScrapeTrackers(in)
	if len(got) != 2 || got[0] != "http://a/announce" || got[1] != "udp://b:80/announce" {
		t.Fatalf("got %v, want [http://a/announce udp://b:80/announce]", got)
	}
}

func TestTrackersFromMetainfo(t *testing.T) {
	if trackersFromMetainfo(nil) != nil {
		t.Fatal("nil metainfo should yield nil")
	}
	mi := &metainfo.MetaInfo{
		Announce:     "http://primary/announce",
		AnnounceList: metainfo.AnnounceList{{"http://t1/announce"}, {"udp://t2:80/announce"}},
	}
	got := trackersFromMetainfo(mi)
	if len(got) != 2 || got[0] != "http://t1/announce" || got[1] != "udp://t2:80/announce" {
		t.Fatalf("got %v", got)
	}
}

func TestCanProbeHealth(t *testing.T) {
	s := NewForTesting() // metainfoDir == "" → no cached .torrent
	var h metainfo.Hash
	if s.CanProbeHealth(h, "") {
		t.Fatal("no magnet and no cached .torrent → should be false")
	}
	if !s.CanProbeHealth(h, "magnet:?xt=urn:btih:abc") {
		t.Fatal("magnet present → should be true")
	}
}

// With no magnet and no scrapeable trackers, probeHealth must NOT overwrite the
// previous snapshot with zeros (private result whose trackers didn't answer).
func TestProbeHealth_NoMagnetKeepsSnapshot(t *testing.T) {
	s := NewForTesting()
	mc, err := NewMetadataCache(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mc.Close() })
	s.SetMetadataCache(mc)
	hash := scrapeTestHash()
	if err := mc.SetHealth(hash.HexString(), 99, 5); err != nil {
		t.Fatal(err)
	}
	s.probeHealth(hash, "")
	if h := mc.GetHealth(hash.HexString()); h == nil || h.Seeders != 99 {
		t.Fatalf("snapshot should be preserved, got %+v", h)
	}
}

// Exercises the UDP branch of scrapeOneTracker without a real tracker: a
// cancelled context makes the scrape return promptly with ok=false.
func TestUDPScrapeTracker_Unreachable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, ok := udpScrapeTracker(ctx, "udp://127.0.0.1:6969/announce", infohash.T(scrapeTestHash())); ok {
		t.Fatal("expected ok=false for an unreachable/cancelled UDP scrape")
	}
}

func TestScrapeOneTracker_BadAndUnsupported(t *testing.T) {
	ih := infohash.T(scrapeTestHash())
	if _, _, ok := scrapeOneTracker(context.Background(), "://nope", ih); ok {
		t.Fatal("unparseable URL → false")
	}
	if _, _, ok := scrapeOneTracker(context.Background(), "ftp://x/announce", ih); ok {
		t.Fatal("unsupported scheme → false")
	}
}

func TestHTTPScrapeTracker_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL + "/announce")
	if _, _, ok := httpScrapeTracker(context.Background(), u, infohash.T(scrapeTestHash())); ok {
		t.Fatal("HTTP 500 → false")
	}
}

// probeHealth prefers the tracker scrape and persists the real number.
func TestProbeHealth_UsesScrape(t *testing.T) {
	hash := scrapeTestHash()
	tr := fakeScrapeTracker(t, hash, 123, 9)

	s := NewForTesting()
	mc, err := NewMetadataCache(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mc.Close() })
	s.SetMetadataCache(mc)

	magnet := "magnet:?xt=urn:btih:" + hash.HexString() + "&tr=" + url.QueryEscape(tr.URL+"/announce")
	s.probeHealth(hash, magnet)

	h := mc.GetHealth(hash.HexString())
	if h == nil || h.Seeders != 123 || h.Peers != 9 {
		t.Fatalf("GetHealth = %+v, want seeders=123 peers=9", h)
	}
}
