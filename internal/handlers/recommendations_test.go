package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/tmdb"
)

func TestBuildSeeds_ResolvesCapsAndWatched(t *testing.T) {
	entries := make([]library.Entry, recMaxSeeds+3)
	for i := range entries {
		entries[i] = library.Entry{Name: "Title"} // matchFn keys off index below
	}
	calls := 0
	matchFn := func(_ context.Context, _ string) (*tmdb.Match, error) {
		calls++
		return &tmdb.Match{TmdbID: calls, Title: "T", Kind: "movie"}, nil
	}
	seeds, watched, disabled := buildSeeds(context.Background(), entries, matchFn)
	if disabled {
		t.Fatal("should not be disabled")
	}
	if len(seeds) != recMaxSeeds {
		t.Errorf("seeds capped at %d, got %d", recMaxSeeds, len(seeds))
	}
	// Every resolved id is recorded as watched, even beyond the seed cap.
	if len(watched) != len(entries) {
		t.Errorf("watched should track all resolved ids (%d), got %d", len(entries), len(watched))
	}
}

func TestBuildSeeds_SkipsUnresolvedAndDisables(t *testing.T) {
	// nil match + generic error are skipped; ErrDisabled aborts with disabled.
	entries := []library.Entry{{Name: "A"}, {Name: "B"}}
	matchFn := func(_ context.Context, name string) (*tmdb.Match, error) {
		if name == "A" {
			return nil, nil // no match
		}
		return nil, errors.New("boom")
	}
	seeds, _, disabled := buildSeeds(context.Background(), entries, matchFn)
	if disabled || len(seeds) != 0 {
		t.Errorf("expected 0 seeds, not disabled; got %d disabled=%v", len(seeds), disabled)
	}

	disabledFn := func(_ context.Context, _ string) (*tmdb.Match, error) { return nil, tmdb.ErrDisabled }
	if _, _, d := buildSeeds(context.Background(), entries, disabledFn); !d {
		t.Error("ErrDisabled must set disabled=true")
	}
}

func m(id int, title string, pop float64) tmdb.Match {
	return tmdb.Match{TmdbID: id, Title: title, PosterURL: "/p.jpg", Popularity: pop, Kind: "movie"}
}

func TestRankRecs_DedupesAndScores(t *testing.T) {
	// Two seeds both recommend id=10 → Score 2, attributed to the first seed.
	seeds := []seed{
		{title: "Seed A", recs: []tmdb.Match{m(10, "Shared", 5), m(11, "OnlyA", 9)}},
		{title: "Seed B", recs: []tmdb.Match{m(10, "Shared", 5)}},
	}
	out := rankRecs(seeds, map[int]bool{}, nil)
	if len(out) != 2 {
		t.Fatalf("expected 2 unique recs, got %d", len(out))
	}
	// Shared (score 2) ranks above OnlyA (score 1) despite lower popularity.
	if out[0].TmdbID != 10 || out[0].Score != 2 {
		t.Errorf("expected id=10 score=2 first, got id=%d score=%d", out[0].TmdbID, out[0].Score)
	}
	if out[0].BecauseOf != "Seed A" {
		t.Errorf("expected attribution to first seed, got %q", out[0].BecauseOf)
	}
}

func TestRankRecs_DropsWatched(t *testing.T) {
	seeds := []seed{{title: "S", recs: []tmdb.Match{m(1, "Seen", 9), m(2, "Fresh", 1)}}}
	out := rankRecs(seeds, map[int]bool{1: true}, nil)
	if len(out) != 1 || out[0].TmdbID != 2 {
		t.Fatalf("watched id=1 must be excluded; got %+v", out)
	}
}

func TestRankRecs_DropsDismissed(t *testing.T) {
	// id=1 is dismissed for this (kind, id) → excluded; id=2 (different) survives.
	seeds := []seed{{title: "S", recs: []tmdb.Match{m(1, "Ignored", 9), m(2, "Fresh", 1)}}}
	dismissed := map[string]bool{library.DismissKey("movie", 1): true}
	out := rankRecs(seeds, map[int]bool{}, dismissed)
	if len(out) != 1 || out[0].TmdbID != 2 {
		t.Fatalf("dismissed movie:1 must be excluded; got %+v", out)
	}
}

func TestRankRecs_TieBreaksByPopularity(t *testing.T) {
	seeds := []seed{{title: "S", recs: []tmdb.Match{m(1, "Low", 2), m(2, "High", 8)}}}
	out := rankRecs(seeds, map[int]bool{}, nil)
	if out[0].TmdbID != 2 {
		t.Errorf("equal score → higher popularity first; got id=%d", out[0].TmdbID)
	}
}

func TestRankRecs_CapsOutput(t *testing.T) {
	var recs []tmdb.Match
	for i := 1; i <= recMaxOut+10; i++ {
		recs = append(recs, m(i, "R", float64(i)))
	}
	out := rankRecs([]seed{{title: "S", recs: recs}}, map[int]bool{}, nil)
	if len(out) != recMaxOut {
		t.Errorf("expected cap at %d, got %d", recMaxOut, len(out))
	}
}

func TestRecommendations_NilClientReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/recommendations", nil)

	Recommendations(nil, nil, nil)(c) // tc == nil → disabled

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestRecommendations_EmptyLibraryReturns200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib, err := library.New(seededPool(t))
	if err != nil {
		t.Fatalf("library.New: %v", err)
	}

	// Isolate from any cached result for the anonymous user (userID 0).
	recCacheMu.Lock()
	delete(recCache, 0)
	recCacheMu.Unlock()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/recommendations", nil)

	// tc is non-nil but the empty library yields no seeds → no TMDB calls happen.
	Recommendations(lib, nil, &tmdb.Client{})(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "[]" {
		t.Errorf("expected empty array, got %s", got)
	}
}

func TestRecCache_PutSweepsExpired(t *testing.T) {
	base := time.Now()
	recCacheMu.Lock()
	recCache[91001] = recCacheEntry{at: base} // stale relative to the Put below
	recCacheMu.Unlock()

	// A later Put (past TTL) must sweep the stale entry while keeping the new one.
	recCachePut(91002, false, []recItem{{Match: m(1, "X", 1)}}, base.Add(recTTL+time.Minute))

	recCacheMu.Lock()
	_, oldThere := recCache[91001]
	_, newThere := recCache[91002]
	recCacheMu.Unlock()
	if oldThere {
		t.Error("expired entry should have been swept on Put")
	}
	if !newThere {
		t.Error("fresh entry should remain after sweep")
	}
}

func TestRecCache_HitAndExpiry(t *testing.T) {
	base := time.Now()
	recCachePut(4242, false, []recItem{{Match: m(1, "X", 1)}}, base)

	if _, ok := recCacheGet(4242, false, base.Add(time.Minute)); !ok {
		t.Error("expected cache hit within TTL")
	}
	if _, ok := recCacheGet(4242, false, base.Add(recTTL+time.Minute)); ok {
		t.Error("expected cache miss after TTL")
	}
}

func TestRecCacheGet_RevealMismatchIsMiss(t *testing.T) {
	now := time.Now()
	recCachePut(91010, true, []recItem{{Match: m(1, "X", 1)}}, now)
	defer func() { recCacheMu.Lock(); delete(recCache, 91010); recCacheMu.Unlock() }()

	if _, ok := recCacheGet(91010, true, now); !ok {
		t.Error("same reveal state should hit")
	}
	if _, ok := recCacheGet(91010, false, now); ok {
		t.Error("a different reveal state must miss — the seed set differs")
	}
}

func TestSeedCandidates_FavoritesFirstSkipAudioCap(t *testing.T) {
	favs := []library.Entry{{Name: "fav-movie", Kind: "video"}, {Name: "fav-song", Kind: "audio"}}
	history := make([]library.Entry, 0, recMaxMatch+10)
	for i := 0; i < recMaxMatch+10; i++ {
		history = append(history, library.Entry{Name: "h", Kind: "video"})
	}
	got := seedCandidates(favs, history)
	if len(got) != recMaxMatch {
		t.Errorf("candidates capped at recMaxMatch (%d), got %d", recMaxMatch, len(got))
	}
	if len(got) == 0 || got[0].Name != "fav-movie" {
		t.Error("favorites must come first")
	}
	for _, e := range got {
		if e.Kind == "audio" {
			t.Error("audio rows must be dropped")
		}
	}
}

func TestFavoriteSeedEntries_NilStreamerIsNil(t *testing.T) {
	if got := favoriteSeedEntries(nil, 1, false); got != nil {
		t.Errorf("nil streamer → nil seeds, got %v", got)
	}
}

func TestFavoriteSeedEntries_ListsNamesPerUser(t *testing.T) {
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	s.SetFavorites(fav)
	if err := fav.Add("Breaking Bad (2008)", "hash1", "magnet:x", "manual", 7); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got := favoriteSeedEntries(s, 7, false)
	if len(got) != 1 || got[0].Name != "Breaking Bad (2008)" {
		t.Errorf("expected the favourite name as a seed entry, got %+v", got)
	}
	if other := favoriteSeedEntries(s, 999, false); len(other) != 0 {
		t.Errorf("favourites are per-user; another user should get none, got %d", len(other))
	}
}
