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
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
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
	// recLibWindow is how many recently-watched rows we pull as seed candidates.
	// Widened from 20: a music binge (audio + music-videos that never match a
	// movie/tv) used to fill the whole window, leaving nothing to seed from.
	recLibWindow = 60
	// recMaxMatch caps how many candidates we resolve via TMDB per build (favorites
	// first, then recent history), bounding TMDB calls + latency even with the
	// wider window. Matches are cached 30d, so this only bites a cold cache.
	recMaxMatch = 30
)

// Per-user result cache: each build fans out up to recMaxSeeds TMDB calls, so we
// memoize the assembled list briefly instead of redoing it on every page open.
var (
	recCacheMu sync.Mutex
	recCache   = map[int]recCacheEntry{}
)

type recCacheEntry struct {
	at     time.Time
	reveal bool // the curtain state this result was built under (see Recommendations)
	items  []recItem
}

// recCacheGet returns the memoized result only when it's fresh AND was built
// under the same reveal-curtain state — flipping the easter egg changes the seed
// set, so a mismatched entry is a miss (rebuilt with the new state).
func recCacheGet(userID int, reveal bool, now time.Time) ([]recItem, bool) {
	recCacheMu.Lock()
	defer recCacheMu.Unlock()
	e, ok := recCache[userID]
	if !ok || now.Sub(e.at) > recTTL || e.reveal != reveal {
		return nil, false
	}
	return e.items, true
}

func recCachePut(userID int, reveal bool, items []recItem, now time.Time) {
	recCacheMu.Lock()
	// Drop expired entries on write so the map can't grow unbounded across users
	// (cheap: len is bounded by the user base, swept only on cache-miss writes).
	for uid, e := range recCache {
		if now.Sub(e.at) > recTTL {
			delete(recCache, uid)
		}
	}
	recCache[userID] = recCacheEntry{at: now, reveal: reveal, items: items}
	recCacheMu.Unlock()
}

// recCacheInvalidate drops the user's memoized result so the next GET rebuilds
// it. Called after a dismiss so the ignored title disappears immediately rather
// than lingering until the TTL elapses.
func recCacheInvalidate(userID int) {
	recCacheMu.Lock()
	delete(recCache, userID)
	recCacheMu.Unlock()
}

// Recommendations — GET /api/recommendations. Builds a personalized list from
// the user's recently-watched library: resolves each watched title to a TMDB
// id+kind (Match is cached 30d) and aggregates TMDB's per-title recommendations,
// dropping anything already watched and ranking by how many watched titles point
// to it. 200+list (possibly empty), 503 when TMDB has no key. Purely additive —
// touches no existing flow.
//
// Seeds come from two sources, FAVORITES FIRST (explicit movie/tv picks, so recs
// survive a watch history dominated by music) then recently-watched titles.
// Audio rows are skipped — music never resolves to a movie/tv id, it only wastes
// a TMDB lookup and crowds out real seeds.
//
// Hidden content & the reveal curtain: hidden-folder titles are reveal-AWARE.
// With the easter egg closed they're dropped before seeding (no "Porque você viu
// <hidden>" leak); with it open they DO seed, like every other hidden listing.
// The result cache is keyed by that curtain state so flipping it rebuilds.
// The user can also explicitly dismiss a recommendation; dismissed titles are
// excluded so a rebuild never resurfaces them.
func Recommendations(lib *library.Store, s *streamer.Streamer, tc *tmdb.Client) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if tc == nil {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(ctx)
		reveal := middleware.IsRevealHidden(ctx)

		now := time.Now()
		if cached, ok := recCacheGet(userID, reveal, now); ok {
			ctx.JSON(http.StatusOK, cached)
			return
		}

		entries, err := lib.List(userID, false, recLibWindow)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Reveal-aware: drop hidden-folder titles only while the curtain is closed.
		if !reveal {
			entries = dropHiddenLibrary(entries, recHiddenHashSet(s, userID))
		}
		// Favorites first, audio dropped, capped — see seedCandidates.
		candidates := seedCandidates(favoriteSeedEntries(s, userID, reveal), entries)

		dismissed, derr := dismissedSet(lib, userID)
		if derr != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": derr.Error()})
			return
		}

		out, disabled := assembleRecs(ctx.Request.Context(), tc, candidates, dismissed)
		if disabled {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
			return
		}
		recCachePut(userID, reveal, out, now)
		ctx.JSON(http.StatusOK, out)
	}
}

// favoriteSeedEntries turns the user's favourites into seed candidates (Name is
// enough — buildSeeds resolves it via TMDB). Reveal-aware: hidden-folder favs are
// included only when the curtain is open. Best-effort: a nil store / error → no
// favourite seeds (recs fall back to the watched library). Kind is left empty so
// buildSeeds' audio skip doesn't apply — a music favourite simply fails to match.
func favoriteSeedEntries(s *streamer.Streamer, userID int, reveal bool) []library.Entry {
	if s == nil || s.Favorites() == nil {
		return nil
	}
	favs, err := s.Favorites().List(userID, false, reveal)
	if err != nil {
		return nil
	}
	out := make([]library.Entry, 0, len(favs))
	for _, f := range favs {
		out = append(out, library.Entry{Name: f.Name})
	}
	return out
}

// assembleRecs resolves the watched entries into seeds, fans out one TMDB
// recommendations call per seed (best-effort), and ranks the result — dropping
// already-watched and dismissed titles. disabled=true ⇒ TMDB has no key.
func assembleRecs(ctx context.Context, tc *tmdb.Client, entries []library.Entry, dismissed map[string]bool) (out []recItem, disabled bool) {
	seeds, watched, disabled := buildSeeds(ctx, entries, tc.Match)
	if disabled {
		return nil, true
	}
	for i := range seeds {
		if recs, rErr := tc.Recommendations(ctx, seeds[i].kind, seeds[i].id); rErr == nil {
			seeds[i].recs = recs // best-effort per seed
		}
	}
	return rankRecs(seeds, watched, dismissed), false
}

// DismissRecommendation — POST /api/recommendations/dismiss with body
// {tmdbId, kind}. Persists a per-user dismissal so the title never reappears in
// the user's recommendations, then invalidates the cached result. Guests are
// blocked upstream by auth.GuestRestrict (POST on a non-whitelisted path → 403).
func DismissRecommendation(lib *library.Store) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if lib == nil {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
			return
		}
		var req struct {
			TmdbID int    `json:"tmdbId"`
			Kind   string `json:"kind"`
		}
		if err := ctx.ShouldBindJSON(&req); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.TmdbID <= 0 || (req.Kind != "movie" && req.Kind != "tv") {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(ctx)
		if err := lib.DismissRecommendation(userID, req.Kind, req.TmdbID); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		recCacheInvalidate(userID) // so the dismissed title drops on the next GET
		ctx.JSON(http.StatusOK, gin.H{"message": "dismissed"})
	}
}

// recHiddenHashSet returns the user's hidden-folder info_hashes, bypassing the
// reveal curtain on purpose: recommendations must never be seeded from hidden
// content regardless of X-JackUI-Reveal-Hidden. nil ⇒ "filter nothing".
func recHiddenHashSet(s *streamer.Streamer, userID int) map[string]bool {
	if s == nil || s.Favorites() == nil {
		return nil
	}
	set, err := s.Favorites().HiddenHashSet(userID, false)
	if err != nil {
		return nil
	}
	return set
}

// dismissedSet loads the user's dismissed recommendations (kind:tmdbID set),
// tolerating a nil store (returns an empty set, never errors).
func dismissedSet(lib *library.Store, userID int) (map[string]bool, error) {
	if lib == nil {
		return map[string]bool{}, nil
	}
	return lib.DismissedRecommendations(userID)
}

// seed is a watched title plus the TMDB recommendations it yielded.
type seed struct {
	kind, title string
	id          int
	recs        []tmdb.Match
}

// seedCandidates orders favourites FIRST (their seed slots should win over a wall
// of recently-watched music) then the watched history, drops audio rows (music
// never resolves to a movie/tv id), and caps the list at recMaxMatch to bound the
// TMDB lookups buildSeeds will do. Pure → unit-testable.
func seedCandidates(favs, history []library.Entry) []library.Entry {
	out := make([]library.Entry, 0, recMaxMatch)
	for _, e := range append(favs, history...) {
		if e.Kind == "audio" {
			continue
		}
		out = append(out, e)
		if len(out) >= recMaxMatch {
			break
		}
	}
	return out
}

// buildSeeds resolves each candidate to a TMDB id+kind (via matchFn, which is
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
// it drops anything in `watched` or `dismissed`, counts how many seeds point to
// each title (Score), attributes the first seed that surfaced it (BecauseOf),
// sorts by Score then popularity, and caps at recMaxOut. `dismissed` is keyed by
// library.DismissKey(kind, tmdbID). Pure → unit-testable without a live TMDB.
func rankRecs(seeds []seed, watched map[int]bool, dismissed map[string]bool) []recItem {
	agg := map[int]*recItem{}
	order := []int{} // preserve first-seen order for stable sort determinism
	for _, s := range seeds {
		for _, m := range s.recs {
			if watched[m.TmdbID] || dismissed[library.DismissKey(m.Kind, m.TmdbID)] {
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
