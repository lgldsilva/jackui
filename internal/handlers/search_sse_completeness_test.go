package handlers

// Tests pinning the search-SSE completeness invariant ("the search shows
// everything it saved"):
//
//  1. The history save is SYNCHRONOUS and happens BEFORE the `done` event —
//     observable order: when the client reads `done`, the rows are committed.
//  2. Every row saved by a search session was also emitted on that session's
//     stream (saved set == emitted set).
//  3. Incognito searches leave nothing in the normally-visible history.
//  4. The convergence pass re-emits only rows not already seen in-session.
//  5. Hits arriving after the emission window are dropped, never accumulated.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/middleware"
)

const (
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hashC = "cccccccccccccccccccccccccccccccccccccccc"
)

func newTestHistoryStore(t *testing.T) *history.Store {
	t.Helper()
	store, err := history.New(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}
	t.Cleanup(store.Close)
	return store
}

// sseJackettServer fakes Jackett: t=indexers lists one configured indexer,
// anything else returns the given results JSON.
func sseJackettServer(t *testing.T, resultsJSON string) *jackett.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "indexers" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprint(w, `<indexers><indexer id="idx1" configured="true"><title>Indexer 1</title></indexer></indexers>`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, resultsJSON)
	}))
	t.Cleanup(srv.Close)
	return jackett.New(srv.URL, "testkey")
}

type sseEvent struct {
	name string
	data map[string]any
}

// readSSE consumes the stream and returns the events in arrival order.
// onEvent (optional) fires synchronously as each event is decoded — used to
// observe ordering against external state (e.g. the DB).
func readSSE(t *testing.T, body *bufio.Scanner, onEvent func(ev sseEvent)) []sseEvent {
	t.Helper()
	var events []sseEvent
	var current string
	for body.Scan() {
		line := body.Text()
		if strings.HasPrefix(line, "event: ") {
			current = strings.TrimPrefix(line, "event: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			var data map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				t.Fatalf("bad SSE data line %q: %v", line, err)
			}
			ev := sseEvent{name: current, data: data}
			events = append(events, ev)
			if onEvent != nil {
				onEvent(ev)
			}
			if current == "done" {
				break
			}
		}
	}
	return events
}

func startSSESearch(t *testing.T, router *gin.Engine, path string) *bufio.Scanner {
	t.Helper()
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	return bufio.NewScanner(resp.Body)
}

// TestSearchSSE_SaveHappensBeforeDone pins the observable order: by the time
// the client reads the `done` event, the live results of THIS session are
// already committed to the history store (the save is synchronous, not a
// goroutine racing past `done`).
func TestSearchSSE_SaveHappensBeforeDone(t *testing.T) {
	store := newTestHistoryStore(t)
	client := sseJackettServer(t, fmt.Sprintf(
		`{"Results":[{"Title":"Live One","Tracker":"Trk","Seeders":5,"Peers":7,"Size":1000,"InfoHash":%q}]}`, hashA))

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(client, store, nil, nil))
	scanner := startSSESearch(t, router, "/api/search/stream?q=savebeforedone")

	var rowsAtDone []history.CachedResult
	events := readSSE(t, scanner, func(ev sseEvent) {
		if ev.name == "done" {
			rowsAtDone, _ = store.Search("savebeforedone", 0, false)
		}
	})

	if len(rowsAtDone) != 1 || rowsAtDone[0].InfoHash != hashA {
		t.Fatalf("at `done` the save must already be committed; rows = %+v", rowsAtDone)
	}
	if events[len(events)-1].name != "done" {
		t.Fatalf("last event = %q, want done", events[len(events)-1].name)
	}
}

// TestSearchSSE_SavedSetWasEmitted pins the core invariant: every row the
// search saved to the history was ALSO emitted as a `result` event on the
// same SSE session.
func TestSearchSSE_SavedSetWasEmitted(t *testing.T) {
	store := newTestHistoryStore(t)
	// Pre-seed the cache with hashB so the session has a cache phase too.
	if err := store.Save("invariant", []jackett.Result{{Title: "Cached B", Tracker: "Trk", InfoHash: hashB, Size: 1}}, 0, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	client := sseJackettServer(t, fmt.Sprintf(
		`{"Results":[{"Title":"Live A","Tracker":"Trk","Seeders":5,"Peers":7,"Size":1000,"InfoHash":%q},{"Title":"Live B dup","Tracker":"Trk","Seeders":9,"Peers":9,"Size":1,"InfoHash":%q}]}`,
		hashA, hashB))

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(client, store, nil, nil))
	scanner := startSSESearch(t, router, "/api/search/stream?q=invariant")

	emitted := map[string]bool{}
	readSSE(t, scanner, func(ev sseEvent) {
		if ev.name == "result" {
			if h, _ := ev.data["infoHash"].(string); h != "" {
				emitted[strings.ToLower(h)] = true
			}
		}
	})

	rows, err := store.Search("invariant", 0, false)
	if err != nil {
		t.Fatalf("store.Search: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected saved rows")
	}
	for _, r := range rows {
		if !emitted[strings.ToLower(r.InfoHash)] {
			t.Errorf("row %q (%s) is in the history but was never emitted on the stream", r.Title, r.InfoHash)
		}
	}
}

// TestSearchSSE_IncognitoLeavesNoVisibleHistory: with the incognito middleware
// active and ?incognito=1, the synchronous save must not surface anything in
// the normally-visible history.
func TestSearchSSE_IncognitoLeavesNoVisibleHistory(t *testing.T) {
	store := newTestHistoryStore(t)
	client := sseJackettServer(t, fmt.Sprintf(
		`{"Results":[{"Title":"Secret","Tracker":"Trk","Seeders":5,"Peers":7,"Size":1000,"InfoHash":%q}]}`, hashC))

	router := gin.New()
	router.Use(middleware.Incognito())
	router.GET("/api/search/stream", SearchSSE(client, store, nil, nil))
	scanner := startSSESearch(t, router, "/api/search/stream?q=secretquery&incognito=1")

	events := readSSE(t, scanner, nil)
	sawLive := false
	for _, ev := range events {
		if ev.name == "result" {
			sawLive = true
		}
	}
	if !sawLive {
		t.Fatal("incognito session must still SEE live results")
	}

	rows, err := store.Search("secretquery", 0, false)
	if err != nil {
		t.Fatalf("store.Search: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("incognito search leaked %d row(s) into the visible history: %+v", len(rows), rows)
	}
}

// TestSearchSSE_ScopedSearchSkipsConvergence: when the search is scoped to
// specific indexers, the convergence pass must not run (it would leak cached
// rows from other providers, the same reason the cache phase is skipped).
func TestSearchSSE_ScopedSearchSkipsConvergence(t *testing.T) {
	store := newTestHistoryStore(t)
	if err := store.Save("scoped", []jackett.Result{{Title: "Other Provider", Tracker: "OtherTrk", InfoHash: hashB, Size: 9}}, 0, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	client := sseJackettServer(t, fmt.Sprintf(
		`{"Results":[{"Title":"Scoped Live","Tracker":"Trk","Seeders":5,"Peers":7,"Size":1000,"InfoHash":%q}]}`, hashA))

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(client, store, nil, nil))
	scanner := startSSESearch(t, router, "/api/search/stream?q=scoped&indexers=idx1")

	events := readSSE(t, scanner, nil)
	for _, ev := range events {
		if ev.name == "result" {
			if h, _ := ev.data["infoHash"].(string); strings.EqualFold(h, hashB) {
				t.Fatal("scoped search leaked a cached row from another provider via convergence")
			}
		}
	}
	done := events[len(events)-1]
	if done.name != "done" {
		t.Fatalf("last event = %q, want done", done.name)
	}
	if conv, _ := done.data["converged"].(float64); conv != 0 {
		t.Errorf("converged = %v, want 0 on a scoped search", conv)
	}
}

// TestEmitConverged_OnlyUnseen: the convergence pass emits exactly the cached
// rows not yet seen in-session, marks them seen, and is idempotent.
func TestEmitConverged_OnlyUnseen(t *testing.T) {
	store := newTestHistoryStore(t)
	seed := []jackett.Result{
		{Title: "Seen Cached", Tracker: "Trk", InfoHash: hashA, Size: 1},
		{Title: "Seen Live", Tracker: "Trk", InfoHash: hashB, Size: 2},
		{Title: "Tail Missed", Tracker: "Trk", InfoHash: hashC, Size: 3},
	}
	if err := store.Save("conv", seed, 0, false); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	state := &liveSearchState{
		c:          c,
		enricher:   &resultEnricher{},
		cachedSeen: map[string]bool{hashA: true},
		liveSeen:   map[string]bool{hashB: true},
	}

	if got := state.emitConverged(store, "conv", 0, false); got != 1 {
		t.Fatalf("emitConverged = %d, want 1 (only the unseen tail)", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Tail Missed") {
		t.Errorf("unseen tail row was not emitted; body: %s", body)
	}
	if strings.Contains(body, "Seen Cached") || strings.Contains(body, "Seen Live") {
		t.Errorf("already-seen rows must not be re-emitted; body: %s", body)
	}
	if !state.liveSeen[hashC] {
		t.Error("converged row must be marked seen")
	}
	// Idempotent: a second pass finds nothing new.
	if got := state.emitConverged(store, "conv", 0, false); got != 0 {
		t.Errorf("second emitConverged = %d, want 0", got)
	}
}

func TestEmitConverged_NilStore(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	state := &liveSearchState{c: c, enricher: &resultEnricher{}, cachedSeen: map[string]bool{}, liveSeen: map[string]bool{}}
	if got := state.emitConverged(nil, "q", 0, false); got != 0 {
		t.Errorf("emitConverged(nil store) = %d, want 0", got)
	}
	if w.Body.Len() != 0 {
		t.Errorf("nil store must emit nothing; body: %s", w.Body.String())
	}
}

// TestHandleHit_AfterEmissionEnded_Drops: a hit landing after the emission
// window (which StreamSearch's wg.Wait makes impossible by construction, but
// this is the guard if that ever changes) must be dropped — NOT accumulated
// into liveResults where it would be saved without having been emitted.
func TestHandleHit_AfterEmissionEnded_Drops(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	state := &liveSearchState{
		c:          c,
		enricher:   &resultEnricher{},
		cachedSeen: map[string]bool{},
		liveSeen:   map[string]bool{},
	}
	state.markEmissionEnded()

	state.handleHit(jackett.IndexerHit{
		IndexerName: "late-indexer",
		Duration:    time.Millisecond,
		Results:     []jackett.Result{{Title: "Late Result", InfoHash: hashA}},
	})

	if len(state.liveResults) != 0 || state.liveCount != 0 {
		t.Fatalf("late hit must be dropped; liveResults=%d liveCount=%d", len(state.liveResults), state.liveCount)
	}
	if w.Body.Len() != 0 {
		t.Errorf("late hit must not write to the stream; body: %s", w.Body.String())
	}
}

func TestPersistEmitted_NilStoreAndEmpty(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	// Must not panic on nil store or empty results.
	persistEmitted(c, nil, "q", []jackett.Result{{Title: "x"}}, 0)
	store := newTestHistoryStore(t)
	persistEmitted(c, store, "q", nil, 0)
	rows, _ := store.Search("q", 0, false)
	if len(rows) != 0 {
		t.Errorf("empty save must persist nothing, got %d row(s)", len(rows))
	}
}

func TestPersistEmitted_SaveErrorIsLoggedNotFatal(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	store, err := history.New(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}
	store.Close() // force Save to fail
	// Must not panic — the error is logged and the stream proceeds to `done`.
	persistEmitted(c, store, "q", []jackett.Result{{Title: "x", InfoHash: hashA}}, 0)
}
