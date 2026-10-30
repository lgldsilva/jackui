package handlers

// Tests for the dual-audio search lane: audioFlags annotation (contract 1),
// prefer_dual ranking boost + dual_only filter (contract 2), and the locked
// boost semantics — dual never outranks a higher quality tier (contract 3,
// docs/PLAN_PTBR_SUBS_DUAL.md D2/D5).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/parser"
)

const (
	titlePlain   = "Movie.2023.1080p.BluRay.x264-GRP"
	titleDual    = "Movie.2023.1080p.BluRay.DUAL.x264-GRP"
	titleDublado = "Movie.2023.720p.WEB-DL.DUBLADO.x264-GRP"
	titlePTBR    = "Movie.2023.1080p.WEB-DL.PT-BR.x264-GRP"
	titleHDTV    = "Movie.2023.720p.HDTV.x264-GRP"
	titleWEBDL   = "Movie.2023.1080p.WEB-DL.x264-GRP"
)

// dualFixture builds a searchResult exactly the way the handlers do.
func dualFixture(title string) searchResult {
	return (&resultEnricher{}).enrich(jackett.Result{Title: title, Tracker: "Trk"}, false)
}

func dualFixtures(titles ...string) []searchResult {
	out := make([]searchResult, 0, len(titles))
	for _, title := range titles {
		out = append(out, dualFixture(title))
	}
	return out
}

func dualTitleList(results []searchResult) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Title
	}
	return out
}

func assertTitleOrder(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("titles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("titles = %v, want %v", got, want)
		}
	}
}

// --- contract 3: rank maps (table tests) ---

func TestResolutionRank_Table(t *testing.T) {
	tests := []struct {
		resolution string
		want       int
	}{
		{"2160p", 4},
		{"1080p", 3},
		{"720p", 2},
		{"480p", 1},
		{"", 0},     // unknown ranks lowest
		{"576p", 0}, // unparsed ranks lowest
	}
	for _, tc := range tests {
		if got := resolutionRank(tc.resolution); got != tc.want {
			t.Errorf("resolutionRank(%q) = %d, want %d", tc.resolution, got, tc.want)
		}
	}
}

func TestSourceRank_Table(t *testing.T) {
	// The locked ladder must be strictly descending:
	// BluRay > WEB-DL > WEBRip > HDTV > DVDRip > CAM/TS > unknown.
	ladder := []string{"BluRay", "WEB-DL", "WEBRip", "HDTV", "DVDRip", "CAM", ""}
	for i := 1; i < len(ladder); i++ {
		hi, lo := sourceRank(ladder[i-1]), sourceRank(ladder[i])
		if hi <= lo {
			t.Errorf("ladder broken: sourceRank(%q)=%d must outrank sourceRank(%q)=%d",
				ladder[i-1], hi, ladder[i], lo)
		}
	}
	// Parser sources the ladder doesn't split out tie with their tier:
	// CAM/TS is one rung; HDRip → WEBRip, DVDScr → DVDRip, Workprint → CAM/TS.
	aliases := [][2]string{
		{"CAM", "TS"},
		{"WEBRip", "HDRip"},
		{"DVDRip", "DVDScr"},
		{"CAM", "Workprint"},
	}
	for _, pair := range aliases {
		if sourceRank(pair[0]) != sourceRank(pair[1]) {
			t.Errorf("sourceRank(%q)=%d must tie with sourceRank(%q)=%d",
				pair[0], sourceRank(pair[0]), pair[1], sourceRank(pair[1]))
		}
	}
	if got := sourceRank("SOMEWHERE"); got != -1 {
		t.Errorf("unlisted source rank = %d, want -1", got)
	}
}

func TestDualSortKeyFor_RemuxPromotesToBluRayTier(t *testing.T) {
	key := dualSortKeyFor(parser.Quality{Resolution: "1080p", Remux: true}, parser.AudioFlags{Dual: true})
	if key.source != sourceRank("BluRay") {
		t.Errorf("remux key.source = %d, want BluRay tier %d", key.source, sourceRank("BluRay"))
	}
	if key.resolution != resolutionRank("1080p") {
		t.Errorf("remux key.resolution = %d, want %d", key.resolution, resolutionRank("1080p"))
	}
	if !key.dual {
		t.Error("remux key.dual = false, want true")
	}
}

// --- contract 3: sort semantics (acceptance fixtures) ---

// The acceptance fixture: 1080p non-dual stays above 720p dual — the boost
// NEVER promotes across a quality tier.
func TestSortPreferDual_NeverPromotesAcrossQualityTier(t *testing.T) {
	results := dualFixtures(titleDublado, titlePlain, titleHDTV)
	sortPreferDual(results, searchResultDualKey)
	assertTitleOrder(t, dualTitleList(results),
		titlePlain,   // 1080p non-dual: higher tier wins over any dual
		titleDublado, // 720p dual: dual flag first WITHIN the 720p tier
		titleHDTV,    // 720p non-dual
	)
}

// 1080p dual moves above 1080p non-dual; the two duals keep their original
// relative order (stable).
func TestSortPreferDual_DualFirstWithinTier(t *testing.T) {
	results := dualFixtures(titleWEBDL, titlePTBR, titleDual)
	// Align sources so all three share the same (resolution, source) tier.
	for i := range results {
		results[i].Quality.Source = "WEB-DL"
	}
	sortPreferDual(results, searchResultDualKey)
	assertTitleOrder(t, dualTitleList(results),
		titlePTBR, // Dual||Dub||PTBR first, original order among them
		titleDual,
		titleWEBDL, // non-dual last within the tier
	)
}

// Everything else (codec, seeders, existing secondary signals) stays in
// original order within the same (tier, flag) group.
func TestSortPreferDual_PreservesOriginalOrderWithinGroup(t *testing.T) {
	sameTier := []string{
		"Movie.2023.720p.HDTV.x264-A",
		"Movie.2023.720p.HDTV.x264-B",
		"Movie.2023.720p.HDTV.x264-C",
	}
	results := dualFixtures(sameTier...)
	// Seeders differ — they must NOT influence the reorder.
	for i := range results {
		results[i].Seeders = 100 - i
	}
	sortPreferDual(results, searchResultDualKey)
	assertTitleOrder(t, dualTitleList(results), sameTier...)
}

// Within the same resolution, the source tier decides: a 1080p BluRay
// non-dual outranks a 1080p WEB-DL dual.
func TestSortPreferDual_SourceRankWithinResolution(t *testing.T) {
	results := dualFixtures(titleDual, titlePlain) // both 1080p BluRay… make one WEB-DL
	results[0].Quality.Source = "WEB-DL"           // titleDual now WEB-DL dual
	sortPreferDual(results, searchResultDualKey)
	assertTitleOrder(t, dualTitleList(results),
		titlePlain, // BluRay tier beats WEB-DL even though the WEB-DL one is dual
		titleDual,
	)
}

// --- contract 1: annotation ---

func TestEnrich_AnnotatesAudioFlags(t *testing.T) {
	e := &resultEnricher{}

	got := e.enrich(jackett.Result{Title: titleDual}, false)
	if !got.AudioFlags.Dual {
		t.Errorf("AudioFlags.Dual = false, want true for %q", titleDual)
	}
	if got.AudioFlags.Dub || got.AudioFlags.PTBR || got.AudioFlags.Multi || got.AudioFlags.Legendado || got.AudioFlags.Latino {
		t.Errorf("only Dual expected, got %+v", got.AudioFlags)
	}

	plain := e.enrich(jackett.Result{Title: titlePlain}, false)
	if plain.AudioFlags != (parser.AudioFlags{}) {
		t.Errorf("flagless title must annotate zero flags, got %+v", plain.AudioFlags)
	}
}

func TestSearch_EmitsAudioFlags(t *testing.T) {
	client := mockJackettClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jackettResultsJSON(
			map[string]interface{}{"Title": titleDual, "Tracker": "Trk", "Size": 10, "Seeders": 1},
			map[string]interface{}{"Title": titlePlain, "Tracker": "Trk", "Size": 10, "Seeders": 2},
		)))
	})

	router := gin.New()
	router.GET("/api/search", Search(client, nil, nil, nil))

	req := httptest.NewRequest("GET", "/api/search?q=dualflags", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body: %s", w.Code, w.Body.String())
	}
	var results []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	for _, res := range results {
		flags, ok := res["audioFlags"].(map[string]interface{})
		if !ok {
			t.Fatalf("result %v has no audioFlags object in payload", res["title"])
		}
		wantDual := strings.Contains(res["title"].(string), "DUAL")
		if flags["dual"] != wantDual {
			t.Errorf("%q audioFlags.dual = %v, want %v", res["title"], flags["dual"], wantDual)
		}
	}
}

func TestSearchSSE_EmitsAudioFlags(t *testing.T) {
	client := sseJackettServer(t, jackettResultsJSON(
		map[string]interface{}{"Title": titleDual, "Tracker": "Trk", "InfoHash": hashA, "Size": 1},
		map[string]interface{}{"Title": titlePlain, "Tracker": "Trk", "InfoHash": hashB, "Size": 1},
	))

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(client, nil, nil, nil))
	scanner := startSSESearch(t, router, "/api/search/stream?q=sseflags")

	emitted := 0
	readSSE(t, scanner, func(ev sseEvent) {
		if ev.name != "result" {
			return
		}
		emitted++
		flags, ok := ev.data["audioFlags"].(map[string]interface{})
		if !ok {
			t.Fatalf("SSE result %v has no audioFlags object", ev.data["title"])
		}
		wantDual := strings.Contains(ev.data["title"].(string), "DUAL")
		if flags["dual"] != wantDual {
			t.Errorf("%q audioFlags.dual = %v, want %v", ev.data["title"], flags["dual"], wantDual)
		}
	})
	if emitted != 2 {
		t.Fatalf("emitted %d result events, want 2", emitted)
	}
}

// --- contract 2: dual_only (plain endpoint) ---

func TestSearch_DualOnlyFiltersResponse(t *testing.T) {
	client := mockJackettClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jackettResultsJSON(
			map[string]interface{}{"Title": titleDual, "Tracker": "Trk", "Size": 10, "Seeders": 1},
			map[string]interface{}{"Title": titlePlain, "Tracker": "Trk", "Size": 10, "Seeders": 99},
			map[string]interface{}{"Title": titleDublado, "Tracker": "Trk", "Size": 10, "Seeders": 1},
		)))
	})

	router := gin.New()
	router.GET("/api/search", Search(client, nil, nil, nil))

	req := httptest.NewRequest("GET", "/api/search?q=dualonly&dual_only=1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body: %s", w.Code, w.Body.String())
	}
	var titles []string
	var results []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, res := range results {
		titles = append(titles, res["title"].(string))
	}
	assertTitleOrder(t, titles, titleDual, titleDublado)
}

// dual_only filters the response payload only — the history keeps the full
// live set (saveHistory runs before the response filter and is untouched).
func TestSearch_DualOnly_KeepsFullHistory(t *testing.T) {
	store := newTestHistoryStore(t)
	client := mockJackettClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jackettResultsJSON(
			map[string]interface{}{"Title": titleDual, "Tracker": "Trk", "InfoHash": hashA, "Size": 10, "Seeders": 1},
			map[string]interface{}{"Title": titlePlain, "Tracker": "Trk", "InfoHash": hashB, "Size": 10, "Seeders": 2},
		)))
	})

	router := gin.New()
	router.GET("/api/search", Search(client, store, nil, nil))

	req := httptest.NewRequest("GET", "/api/search?q=dualonlyhistory&dual_only=1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body: %s", w.Code, w.Body.String())
	}

	var results []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 1 || results[0]["title"] != titleDual {
		t.Fatalf("filtered response = %v, want only %q", results, titleDual)
	}

	// saveHistory is async: poll until the full set (2 rows) is committed.
	rows := waitForHistoryRows(t, store, "dualonlyhistory", 2)
	if len(rows) != 2 {
		t.Fatalf("history has %d row(s), want the full set of 2 — dual_only must not prune history", len(rows))
	}
}

// --- contract 2: dual_only (SSE endpoint) ---

func TestSearchSSE_DualOnly_FiltersResults(t *testing.T) {
	client := sseJackettServer(t, jackettResultsJSON(
		map[string]interface{}{"Title": titleDual, "Tracker": "Trk", "InfoHash": hashA, "Size": 1},
		map[string]interface{}{"Title": titlePlain, "Tracker": "Trk", "InfoHash": hashB, "Size": 1},
		map[string]interface{}{"Title": titleDublado, "Tracker": "Trk", "InfoHash": hashC, "Size": 1},
	))

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(client, nil, nil, nil))
	scanner := startSSESearch(t, router, "/api/search/stream?q=ssedualonly&dual_only=1")

	var titles []string
	var done map[string]any
	readSSE(t, scanner, func(ev sseEvent) {
		switch ev.name {
		case "result":
			titles = append(titles, ev.data["title"].(string))
		case "done":
			done = ev.data
		}
	})
	assertTitleOrder(t, titles, titleDual, titleDublado)
	if live, _ := done["live"].(float64); live != 2 {
		t.Errorf("done.live = %v, want 2 (filtered count)", done["live"])
	}
}

// The SSE stream hides non-dual results, but the saved set is the FULL set:
// dual_only is a response-payload filter and must not corrupt history.
func TestSearchSSE_DualOnly_KeepsFullHistory(t *testing.T) {
	store := newTestHistoryStore(t)
	client := sseJackettServer(t, jackettResultsJSON(
		map[string]interface{}{"Title": titleDual, "Tracker": "Trk", "InfoHash": hashA, "Size": 1},
		map[string]interface{}{"Title": titlePlain, "Tracker": "Trk", "InfoHash": hashB, "Size": 1},
	))

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(client, store, nil, nil))
	scanner := startSSESearch(t, router, "/api/search/stream?q=ssedualonlyhistory&dual_only=1")

	emitted := 0
	readSSE(t, scanner, func(ev sseEvent) {
		if ev.name == "result" {
			emitted++
		}
	})
	if emitted != 1 {
		t.Fatalf("emitted %d result event(s), want 1 (only the dual one)", emitted)
	}

	// persistEmitted is synchronous BEFORE done, so the full set is committed
	// once the stream returns.
	rows, err := store.Search("ssedualonlyhistory", 0, false)
	if err != nil {
		t.Fatalf("store.Search: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("history has %d row(s), want the full set of 2 — dual_only must not prune history", len(rows))
	}
}

// The cache phase honors prefer_dual too: the cached batch is sorted before
// emission (exercises cachedResultDualKey).
func TestSearchSSE_PreferDual_SortsCachedBatch(t *testing.T) {
	store := newTestHistoryStore(t)
	if err := store.Save("cachedualsort", []jackett.Result{
		{Title: titleDublado, Tracker: "Trk", InfoHash: hashA, Size: 1},
		{Title: titlePlain, Tracker: "Trk", InfoHash: hashB, Size: 1},
		{Title: titleHDTV, Tracker: "Trk", InfoHash: hashC, Size: 1},
	}, 0, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Live phase returns nothing — the emitted order comes from the cache batch.
	client := sseJackettServer(t, `{"Results":[]}`)

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(client, store, nil, nil))
	scanner := startSSESearch(t, router, "/api/search/stream?q=cachedualsort&prefer_dual=1")

	var titles []string
	readSSE(t, scanner, func(ev sseEvent) {
		if ev.name == "result" {
			titles = append(titles, ev.data["title"].(string))
		}
	})
	assertTitleOrder(t, titles, titlePlain, titleDublado, titleHDTV)
}

// The convergence pass applies the same options: prefer_dual sorts the fresh
// tail, dual_only hides non-dual rows from the stream (they keep their seen
// mark and stay in the history).
func TestEmitConverged_PreferDualSortsAndDualOnlyFilters(t *testing.T) {
	store := newTestHistoryStore(t)
	if err := store.Save("convdual", []jackett.Result{
		{Title: "Seen Earlier", Tracker: "Trk", InfoHash: hashC, Size: 1}, // live phase already emitted this one
		{Title: titleDublado, Tracker: "Trk", InfoHash: hashA, Size: 1},   // 720p dual, unseen tail
		{Title: titlePlain, Tracker: "Trk", InfoHash: hashB, Size: 1},     // 1080p plain, unseen tail
	}, 0, false); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	state := &liveSearchState{
		c:          c,
		enricher:   &resultEnricher{},
		opts:       dualEmitOpts{preferDual: true, dualOnly: true},
		cachedSeen: map[string]bool{},
		// Simulate a live phase (the pass is skipped for cache-only sessions):
		// hashC was emitted live, the dual/plain tail was not.
		liveSeen: map[string]bool{hashC: true},
	}

	if got := state.emitConverged(store, "convdual", 0, false); got != 1 {
		t.Fatalf("emitConverged = %d, want 1 (only the dual row)", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, titleDublado) {
		t.Errorf("dual row missing from stream; body: %s", body)
	}
	if strings.Contains(body, titlePlain) {
		t.Errorf("non-dual row must stay hidden; body: %s", body)
	}
	if !state.liveSeen[hashB] {
		t.Error("filtered row must keep its seen mark")
	}
	// The full seed set stays in the history (dual_only never prunes it).
	rows, err := store.Search("convdual", 0, false)
	if err != nil {
		t.Fatalf("store.Search: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("history has %d row(s), want the full set of 3", len(rows))
	}
}

// --- contract 2: prefer_dual (endpoint level) ---

func TestSearch_PreferDual_ReordersWithinTier(t *testing.T) {
	client := mockJackettClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jackettResultsJSON(
			map[string]interface{}{"Title": titleDublado, "Tracker": "Trk", "Size": 10, "Seeders": 50},
			map[string]interface{}{"Title": titlePlain, "Tracker": "Trk", "Size": 10, "Seeders": 1},
			map[string]interface{}{"Title": titleHDTV, "Tracker": "Trk", "Size": 10, "Seeders": 99},
		)))
	})

	router := gin.New()
	router.GET("/api/search", Search(client, nil, nil, nil))

	req := httptest.NewRequest("GET", "/api/search?q=preferdual&prefer_dual=1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body: %s", w.Code, w.Body.String())
	}
	var titles []string
	var results []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, res := range results {
		titles = append(titles, res["title"].(string))
	}
	assertTitleOrder(t, titles, titlePlain, titleDublado, titleHDTV)
}

// Without prefer_dual the response order is byte-identical to the incoming
// order — no sorting change at all.
func TestSearch_PreferDual_AbsentKeepsOriginalOrder(t *testing.T) {
	client := mockJackettClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jackettResultsJSON(
			map[string]interface{}{"Title": titleDublado, "Tracker": "Trk", "Size": 10, "Seeders": 1},
			map[string]interface{}{"Title": titlePlain, "Tracker": "Trk", "Size": 10, "Seeders": 2},
			map[string]interface{}{"Title": titleHDTV, "Tracker": "Trk", "Size": 10, "Seeders": 3},
		)))
	})

	router := gin.New()
	router.GET("/api/search", Search(client, nil, nil, nil))

	req := httptest.NewRequest("GET", "/api/search?q=orderunchanged", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var titles []string
	var results []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, res := range results {
		titles = append(titles, res["title"].(string))
	}
	assertTitleOrder(t, titles, titleDublado, titlePlain, titleHDTV)
}

func TestSearchSSE_PreferDual_SortsBatchWithinTier(t *testing.T) {
	client := sseJackettServer(t, jackettResultsJSON(
		map[string]interface{}{"Title": titleDublado, "Tracker": "Trk", "InfoHash": hashA, "Size": 1},
		map[string]interface{}{"Title": titlePlain, "Tracker": "Trk", "InfoHash": hashB, "Size": 1},
		map[string]interface{}{"Title": titleHDTV, "Tracker": "Trk", "InfoHash": hashC, "Size": 1},
	))

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(client, nil, nil, nil))
	scanner := startSSESearch(t, router, "/api/search/stream?q=ssepreferdual&prefer_dual=1")

	var titles []string
	readSSE(t, scanner, func(ev sseEvent) {
		if ev.name == "result" {
			titles = append(titles, ev.data["title"].(string))
		}
	})
	assertTitleOrder(t, titles, titlePlain, titleDublado, titleHDTV)
}

// --- contract 2: both flags together (plain endpoint) ---

func TestSearch_DualOnlyThenPreferDual(t *testing.T) {
	client := mockJackettClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jackettResultsJSON(
			map[string]interface{}{"Title": titleDublado, "Tracker": "Trk", "Size": 10, "Seeders": 1},
			map[string]interface{}{"Title": titleDual, "Tracker": "Trk", "Size": 10, "Seeders": 2},
			map[string]interface{}{"Title": titlePlain, "Tracker": "Trk", "Size": 10, "Seeders": 3},
		)))
	})

	router := gin.New()
	router.GET("/api/search", Search(client, nil, nil, nil))

	req := httptest.NewRequest("GET", "/api/search?q=dualcombo&prefer_dual=1&dual_only=1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var titles []string
	var results []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, res := range results {
		titles = append(titles, res["title"].(string))
	}
	// Plain filtered out, then the duals sorted within their tier.
	assertTitleOrder(t, titles, titleDual, titleDublado)
}

// waitForHistoryRows polls the store until the plain handler's async save
// (saveHistory fires a goroutine) commits — there is no synchronization
// handle on it, so a bounded poll is the honest way to await it.
func waitForHistoryRows(t *testing.T, store *history.Store, query string, want int) []history.CachedResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, err := store.Search(query, 0, false)
		if err != nil {
			t.Fatalf("store.Search: %v", err)
		}
		if len(rows) >= want {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("history has %d row(s) for %q, want %d", len(rows), query, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSearchSSE_DualOnly_SkipsFilteredRowsInConvergence(t *testing.T) {
	store := newTestHistoryStore(t)
	// Pre-seed the cache so the cache phase emits the dual row and hides the
	// plain one; the live phase then returns BOTH (same hashes → deduped).
	if err := store.Save("convdualonly", []jackett.Result{
		{Title: titleDual, Tracker: "Trk", InfoHash: hashA, Size: 1},
		{Title: titlePlain, Tracker: "Trk", InfoHash: hashB, Size: 1},
	}, 0, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	client := sseJackettServer(t, jackettResultsJSON(
		map[string]interface{}{"Title": titleDual, "Tracker": "Trk", "InfoHash": hashA, "Size": 1},
		map[string]interface{}{"Title": titlePlain, "Tracker": "Trk", "InfoHash": hashB, "Size": 1},
	))

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(client, store, nil, nil))
	scanner := startSSESearch(t, router, "/api/search/stream?q=convdualonly&dual_only=1")

	var titles []string
	readSSE(t, scanner, func(ev sseEvent) {
		if ev.name == "result" {
			titles = append(titles, ev.data["title"].(string))
		}
	})
	// The plain row is hidden exactly once — in the cache phase — and must
	// never resurface via the live phase or the convergence pass.
	assertTitleOrder(t, titles, titleDual)
}
