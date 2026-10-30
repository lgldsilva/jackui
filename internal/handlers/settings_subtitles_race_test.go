package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
)

// TestSubtitlesSettingsRace_SaveVsAutoFetchCycles is the data-race regression
// proof for the subtitle settings: SubtitlesUpdateSettings swaps the live
// cfg.Subtitles section while a REAL AutoFetcher — wired exactly like
// cmd/server/wiring.go initSubtitlesAuto, with the settings closure reading the
// shared config LIVE — runs StartTorrent/Status cycles, and status/settings GET
// pollers read the same section. Before the synchronized
// SubtitlesSnapshot/SetSubtitles access this test fails `go test -race`:
// unsynchronized field writes racing closure reads on goroutines that live
// outside gin's per-request recovery.
//
// The OpenSubtitles client is DISABLED (no API key) so every cycle stays
// offline — the race surface (the settings closure, the auto-fetch gate, the
// section swap itself) is fully exercised with zero network.
func TestSubtitlesSettingsRace_SaveVsAutoFetchCycles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg, path := subtitlesLoadedConfigForTest(t)

	s := streamer.NewForTesting()
	hash, cleanup := s.SeedActiveForTesting("race-sub-settings", time.Now())
	t.Cleanup(cleanup)

	pool := dbtest.NewDB(t)
	links, err := subtitles.NewLinksStore(pool)
	if err != nil {
		t.Fatalf("NewLinksStore: %v", err)
	}
	client := subtitles.New("", "", "", t.TempDir()) // disabled: offline cycles
	fetcher := subtitles.NewAutoFetcher(
		client,
		links,
		nil, // nil TMDB tolerated: tier-2 degrades to the plain query search
		func() (string, bool) { // the EXACT wiring.go closure shape
			snap := cfg.SubtitlesSnapshot()
			return snap.DefaultLanguages, snap.AutoFetch
		},
	)

	r := gin.New()
	r.POST("/api/settings/subtitles", SubtitlesUpdateSettings(cfg, path))
	r.GET("/api/settings/subtitles", SubtitlesGetSettings(cfg))
	r.GET("/api/stream/:hash/:file/subtitles/auto",
		SubtitlesAutoStatus(s, nil, links, client, fetcher, cfg))

	postBodies := []string{
		`{"default_languages":"en-US,es","auto_fetch":true,"burn_default":true}`,
		`{"default_languages":"pt-BR,pt","auto_fetch":false,"burn_default":false}`,
	}

	// All workers spin for the SAME wall-clock window (not fixed counts): a
	// race needs true temporal overlap between writers and readers, and the
	// file-write/marshal side is orders of magnitude faster than a DB cycle.
	// The fetch workers rotate over a FIXED hash set — after the first pass the
	// rows are failed (never retried), so DB load stays bounded while start()
	// keeps exercising the settings closure every cycle.
	deadline := time.Now().Add(1200 * time.Millisecond)
	fetchHashes := make([]metainfo.Hash, 64)
	for i := range fetchHashes {
		fetchHashes[i] = metainfo.HashBytes([]byte(fmt.Sprintf("race-%d", i)))
	}
	var wg sync.WaitGroup
	start := make(chan struct{})

	// Admin saves: swap the whole live section while every reader runs.
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; time.Now().Before(deadline); i++ {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/settings/subtitles",
					strings.NewReader(postBodies[(g+i)%len(postBodies)])))
				if w.Code != http.StatusOK {
					t.Errorf("POST settings = %d, want 200; body: %s", w.Code, w.Body.String())
					return
				}
			}
		}(g)
	}

	// Fetch workers: StartTorrent/Status cycles through the real AutoFetcher —
	// start() reads the settings closure on EVERY cycle, Status re-reads it.
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			meta := &fakeTorrentMeta{}
			for i := 0; time.Now().Before(deadline); i++ {
				h := fetchHashes[(g*len(fetchHashes)/2+i)%len(fetchHashes)]
				fetcher.StartTorrent(meta, h, 0)
				_ = fetcher.Status(context.Background(), h.HexString(), 0)
			}
		}(g)
	}

	// Pollers: the status endpoint (auto-fetch gate + AutoStatus projection)
	// and the settings GET (whole-section projection), like the web's 1–2s
	// per-player polling — here back-to-back for the whole window.
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; time.Now().Before(deadline); i++ {
				w := httptest.NewRecorder()
				if i%2 == 0 {
					r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
						"/api/stream/"+hash.HexString()+"/0/subtitles/auto", nil))
				} else {
					r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings/subtitles", nil))
				}
				if w.Code != http.StatusOK {
					t.Errorf("poll = %d, want 200; body: %s", w.Code, w.Body.String())
					return
				}
			}
		}(g)
	}

	close(start)
	wg.Wait()
}
