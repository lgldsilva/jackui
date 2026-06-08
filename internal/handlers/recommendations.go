package handlers

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/tmdb"
)

// recItem is a recommended title plus why it surfaced. Embeds tmdb.Match so the
// JSON shape matches the trending endpoint (the UI reuses the same poster grid).
type recItem struct {
	tmdb.Match
	BecauseOf string `json:"becauseOf"` // the watched title that seeded it
	Score     int    `json:"score"`     // how many watched titles recommended it
}

const (
	recMaxSeeds = 8  // cap watched titles probed → caps TMDB calls per build
	recMaxOut   = 30 // cap returned recommendations
	recTTL      = time.Hour
)

// Per-user result cache: each build fans out up to recMaxSeeds TMDB calls, so we
// memoize the assembled list briefly instead of redoing it on every page open.
var (
	recCacheMu sync.Mutex
	recCache   = map[int]recCacheEntry{}
)

type recCacheEntry struct {
	at    time.Time
	items []recItem
}

func recCacheGet(userID int, now time.Time) ([]recItem, bool) {
	recCacheMu.Lock()
	defer recCacheMu.Unlock()
	e, ok := recCache[userID]
	if !ok || now.Sub(e.at) > recTTL {
		return nil, false
	}
	return e.items, true
}

func recCachePut(userID int, items []recItem, now time.Time) {
	recCacheMu.Lock()
	// Drop expired entries on write so the map can't grow unbounded across users
	// (cheap: len is bounded by the user base, swept only on cache-miss writes).
	for uid, e := range recCache {
		if now.Sub(e.at) > recTTL {
			delete(recCache, uid)
		}
	}
	recCache[userID] = recCacheEntry{at: now, items: items}
	recCacheMu.Unlock()
}

// Recommendations — GET /api/recommendations. Builds a personalized list from
// the user's recently-watched library: resolves each watched title to a TMDB
// id+kind (Match is cached 30d) and aggregates TMDB's per-title recommendations,
// dropping anything already watched and ranking by how many watched titles point
// to it. 200+list (possibly empty), 503 when TMDB has no key. Purely additive —
// touches no existing flow.
func Recommendations(lib *library.Store, tc *tmdb.Client) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if tc == nil {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(ctx)

		now := time.Now()
		if cached, ok := recCacheGet(userID, now); ok {
			ctx.JSON(http.StatusOK, cached)
			return
		}

		entries, err := lib.List(userID, false, 20)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		rctx := ctx.Request.Context()
		seeds, watched, disabled := buildSeeds(rctx, entries, tc.Match)
		if disabled {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
			return
		}

		for i := range seeds {
			if recs, rErr := tc.Recommendations(rctx, seeds[i].kind, seeds[i].id); rErr == nil {
				seeds[i].recs = recs // best-effort per seed
			}
		}

		out := rankRecs(seeds, watched)
		recCachePut(userID, out, now)
		ctx.JSON(http.StatusOK, out)
	}
}

// seed is a watched title plus the TMDB recommendations it yielded.
type seed struct {
	kind, title string
	id          int
	recs        []tmdb.Match
}

// buildSeeds resolves each watched entry to a TMDB id+kind (via matchFn, which is
// cached 30d) into recommendation seeds (capped at recMaxSeeds) plus the set of
// already-watched tmdbIDs. Unresolved titles are skipped. disabled=true means
// TMDB reported no key, so the caller should 503.
func buildSeeds(ctx context.Context, entries []library.Entry, matchFn func(context.Context, string) (*tmdb.Match, error)) (seeds []seed, watched map[int]bool, disabled bool) {
	watched = map[int]bool{}
	for _, e := range entries {
		mm, err := matchFn(ctx, e.Name)
		if err != nil {
			if errors.Is(err, tmdb.ErrDisabled) {
				return nil, nil, true
			}
			continue // unresolved title → skip
		}
		if mm == nil || mm.TmdbID <= 0 {
			continue
		}
		watched[mm.TmdbID] = true
		if len(seeds) < recMaxSeeds {
			seeds = append(seeds, seed{kind: mm.Kind, title: mm.Title, id: mm.TmdbID})
		}
	}
	return seeds, watched, false
}

// rankRecs aggregates per-seed recommendations into a deduped, ranked list:
// it drops anything in `watched`, counts how many seeds point to each title
// (Score), attributes the first seed that surfaced it (BecauseOf), sorts by
// Score then popularity, and caps at recMaxOut. Pure → unit-testable without a
// live TMDB.
func rankRecs(seeds []seed, watched map[int]bool) []recItem {
	agg := map[int]*recItem{}
	order := []int{} // preserve first-seen order for stable sort determinism
	for _, s := range seeds {
		for _, m := range s.recs {
			if watched[m.TmdbID] {
				continue
			}
			if cur, ok := agg[m.TmdbID]; ok {
				cur.Score++
				continue
			}
			agg[m.TmdbID] = &recItem{Match: m, BecauseOf: s.title, Score: 1}
			order = append(order, m.TmdbID)
		}
	}
	out := make([]recItem, 0, len(order))
	for _, id := range order {
		out = append(out, *agg[id])
	}
	// Most-recommended first (pointed to by several watched titles), tie-break by
	// popularity.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Popularity > out[j].Popularity
	})
	if len(out) > recMaxOut {
		out = out[:recMaxOut]
	}
	return out
}
