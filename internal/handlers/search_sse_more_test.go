package handlers

import (
	"encoding/base32"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/jackett"
)

type mockEnricher struct {
	*resultEnricher
}

func (m *mockEnricher) enrich(r jackett.Result, cached bool) gin.H {
	return gin.H{"title": r.Title, "cached": cached}
}

func TestParseSearchParams_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/search/stream", nil)

	query, category, indexers := parseSearchParams(c)
	if query != "" {
		t.Errorf("query = %q, want empty", query)
	}
	if category != "" {
		t.Errorf("category = %q, want empty", category)
	}
	if len(indexers) != 0 {
		t.Errorf("indexers = %v, want empty", indexers)
	}
}

func TestParseSearchParams_WithQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/search/stream?q=test&category=movies&indexers=idx1,idx2", nil)

	query, category, indexers := parseSearchParams(c)
	if query != "test" {
		t.Errorf("query = %q, want 'test'", query)
	}
	if category != "movies" {
		t.Errorf("category = %q, want 'movies'", category)
	}
	if len(indexers) != 2 {
		t.Errorf("indexers = %v, want [idx1 idx2]", indexers)
	}
}

func TestParseSearchParams_WithSpaces(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// Use plus signs for spaces in URL encoding
	c.Request = httptest.NewRequest("GET", "/api/search/stream?q=test&indexers=+idx1+,+idx2+", nil)

	_, _, indexers := parseSearchParams(c)
	if len(indexers) != 2 {
		t.Errorf("indexers = %v, want [idx1 idx2]", indexers)
	}
}

func TestEmitCachedResults_NilStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	enricher := &resultEnricher{}
	seen, count := emitCachedResults(c, nil, "query", 0, false, nil, enricher)
	if len(seen) != 0 {
		t.Errorf("seen = %v, want empty", seen)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestHandleHit_Error(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setSSEHeaders(c)

	state := &liveSearchState{
		c:        c,
		enricher: &resultEnricher{},
	}

	hit := jackett.IndexerHit{
		IndexerName: "test-indexer",
		Duration:    100 * time.Millisecond,
		Err:         http.ErrAbortHandler,
	}
	state.handleHit(hit)

	if state.indexersDone != 1 {
		t.Errorf("indexersDone = %d, want 1", state.indexersDone)
	}
	if state.indexersFailed != 1 {
		t.Errorf("indexersFailed = %d, want 1", state.indexersFailed)
	}
}

func TestHandleHit_Results(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setSSEHeaders(c)

	state := &liveSearchState{
		c:        c,
		enricher: &resultEnricher{},
		cachedSeen: map[string]bool{},
		liveSeen:   map[string]bool{},
	}

	hit := jackett.IndexerHit{
		IndexerName: "test-indexer",
		Duration:    100 * time.Millisecond,
		Results: []jackett.Result{
			{Title: "Result 1", InfoHash: "aaa"},
			{Title: "Result 2", InfoHash: "bbb"},
		},
	}
	state.handleHit(hit)

	if state.liveCount != 2 {
		t.Errorf("liveCount = %d, want 2", state.liveCount)
	}
	if len(state.liveResults) != 2 {
		t.Errorf("liveResults = %d, want 2", len(state.liveResults))
	}
}

func TestHandleHit_Dedupe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setSSEHeaders(c)

	state := &liveSearchState{
		c:        c,
		enricher: &resultEnricher{},
		cachedSeen: map[string]bool{"dupe": true},
		liveSeen:   map[string]bool{},
	}

	hit := jackett.IndexerHit{
		IndexerName: "test-indexer",
		Duration:    50 * time.Millisecond,
		Results: []jackett.Result{
			{Title: "Cached Dupe", InfoHash: "dupe"},
			{Title: "New Result", InfoHash: "new"},
		},
	}
	state.handleHit(hit)

	if state.liveCount != 1 {
		t.Errorf("liveCount = %d, want 1 (dupe should be deduped)", state.liveCount)
	}
}

func TestHandleHit_EmptyInfoHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setSSEHeaders(c)

	state := &liveSearchState{
		c:        c,
		enricher: &resultEnricher{},
		cachedSeen: map[string]bool{},
		liveSeen:   map[string]bool{},
	}

	hit := jackett.IndexerHit{
		IndexerName: "test-indexer",
		Duration:    50 * time.Millisecond,
		Results: []jackett.Result{
			{Title: "No InfoHash", InfoHash: ""},
		},
	}
	state.handleHit(hit)

	if state.liveCount != 1 {
		t.Errorf("liveCount = %d, want 1 (empty infohash still counted)", state.liveCount)
	}
}

// TestHandleHit_DedupesAcrossHashEncodings is the defense-in-depth guard for the
// SSE dedup layer. Item #1 canonicalizes infoHash at the Jackett-client source,
// so the normal flow never reaches here with divergent encodings — but a legacy
// cache row (saved before canonicalization), or any future code path that feeds
// handleHit directly, could. The same torrent expressed as UPPER-case hex, as
// base32, and as magnet-only must collapse to ONE emitted result, never several
// duplicate cards.
func TestHandleHit_DedupesAcrossHashEncodings(t *testing.T) {
	const canonical = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	raw, err := hex.DecodeString(canonical)
	if err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
	b32 := base32.StdEncoding.EncodeToString(raw)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setSSEHeaders(c)

	state := &liveSearchState{
		c:          c,
		enricher:   &resultEnricher{},
		cachedSeen: map[string]bool{},
		liveSeen:   map[string]bool{},
	}

	hit := jackett.IndexerHit{
		IndexerName: "test-indexer",
		Duration:    50 * time.Millisecond,
		Results: []jackett.Result{
			{Title: "upper hex", InfoHash: strings.ToUpper(canonical)},
			{Title: "base32", InfoHash: b32},
			{Title: "magnet only", MagnetURI: "magnet:?xt=urn:btih:" + canonical},
		},
	}
	state.handleHit(hit)

	if state.liveCount != 1 {
		t.Errorf("liveCount = %d, want 1 — same torrent across 3 encodings must dedupe", state.liveCount)
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
