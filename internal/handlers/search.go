package handlers

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/parser"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// searchResult is what /api/search returns per item. Extends
// jackett.Result with server-side computed enrichments:
//
//   - Cached: came from the local cache instead of a live Jackett query
//   - Quality: release parse (resolution, codec, source, audio)
//   - AudioFlags: audio-language signals parsed from the title (dual/dub/ptbr, ...)
//   - Playable / MediaKind: "is this playable audio/video" heuristic (used to
//     live in web/src/lib/playable.ts; moved here for a single source)
//   - IsFavorited / IsDownloaded: cheap joins against the user's favorites/downloads,
//     sparing ResultCard from keeping module-scope Sets
type searchResult struct {
	jackett.Result
	Cached       bool              `json:"cached"`
	Quality      parser.Quality    `json:"quality"`
	AudioFlags   parser.AudioFlags `json:"audioFlags"`
	Playable     bool              `json:"playable"`
	MediaKind    parser.MediaKind  `json:"mediaKind"`
	IsFavorited  bool              `json:"isFavorited"`
	IsDownloaded bool              `json:"isDownloaded"`
}

// resultEnricher preloads the user's favorites/downloads sets once
// and enriches N results without N queries. Built by buildEnricher().
type resultEnricher struct {
	favHashes map[string]bool
	dlHashes  map[string]bool
}

// buildEnricher preloads the user's favorites/downloads sets once
// and enriches N results without N queries. With the curtain CLOSED
// (reveal=false) hidden-folder favourites are subtracted from the fav set so a
// hidden title never shows the heart badge in search / history replay — the
// badge is a per-title state leak even when the title itself isn't shown.
func buildEnricher(favs *streamer.FavoritesStore, dls *downloads.Store, userID int, includeAll, reveal bool) *resultEnricher {
	e := &resultEnricher{
		favHashes: map[string]bool{},
		dlHashes:  map[string]bool{},
	}
	if favs != nil {
		if m, err := favs.HashSetForUser(userID, includeAll); err == nil {
			e.favHashes = m
		}
		if !reveal {
			if curtain, err := favs.HiddenFavorites(userID, includeAll); err == nil {
				for h := range curtain.Hashes {
					delete(e.favHashes, strings.ToLower(strings.TrimSpace(h)))
				}
			}
		}
	}
	if dls != nil {
		if m, err := dls.HashSetForUser(userID, includeAll); err == nil {
			e.dlHashes = m
		}
	}
	return e
}

func (e *resultEnricher) enrich(r jackett.Result, cached bool) searchResult {
	q := parser.Parse(r.Title)
	out := searchResult{
		Result:     r,
		Cached:     cached,
		Quality:    q,
		AudioFlags: parser.DetectAudioFlags(r.Title),
		Playable:   parser.IsPlayable(r.Title, r.CategoryID, r.MagnetURI, q.Resolution),
		MediaKind:  parser.DetectKind(r.Title, r.CategoryID),
	}
	if e != nil && r.InfoHash != "" {
		out.IsFavorited = e.favHashes[r.InfoHash]
		out.IsDownloaded = e.dlHashes[r.InfoHash]
	}
	return out
}

func Search(client *jackett.Client, store *history.Store, favs *streamer.FavoritesStore, dls *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := c.Query("q")
		if query == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrQueryRequired)
			return
		}
		indexers := parseIndexers(c)
		opts := parseDualOpts(c)
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && queryBool(c, "all")
		enricher := buildEnricher(favs, dls, userID, includeAll, middleware.IsRevealHidden(c))

		liveResults, liveErr := client.Search(query, c.Query("category"), indexers)
		saveHistory(store, query, liveResults, userID, liveErr, c)
		merged := searchMerged(store, query, userID, includeAll, liveResults, enricher)

		if liveErr != nil && len(merged) == 0 {
			httpshared.RespondError(c, http.StatusBadGateway, liveErr)
			return
		}
		// prefer_dual/dual_only shape the response payload only — saveHistory
		// above already persisted the full live set, so history is unaffected.
		if opts.dualOnly {
			merged = filterDualOnly(merged)
		}
		if opts.preferDual {
			sortPreferDual(merged, searchResultDualKey)
		}
		c.JSON(http.StatusOK, merged)
	}
}

func parseIndexers(c *gin.Context) []string {
	raw := c.Query("indexers")
	if raw == "" {
		return nil
	}
	var indexers []string
	for _, idx := range strings.Split(raw, ",") {
		if idx = strings.TrimSpace(idx); idx != "" {
			indexers = append(indexers, idx)
		}
	}
	return indexers
}

func saveHistory(store *history.Store, query string, liveResults []jackett.Result, userID int, liveErr error, c *gin.Context) {
	if liveErr != nil || store == nil || len(liveResults) == 0 {
		return
	}
	incognito := middleware.IsIncognito(c)
	go func() { _ = store.Save(query, liveResults, userID, incognito) }()
}

func searchMerged(store *history.Store, query string, userID int, includeAll bool, liveResults []jackett.Result, enricher *resultEnricher) []searchResult {
	var cached []history.CachedResult
	if store != nil {
		cached, _ = store.Search(query, userID, includeAll)
	}
	return mergeResults(liveResults, cached, enricher)
}

// mergeResults combines live and cached results.
// Live results are added first; cached results only appear if their infoHash is not already present.
func mergeResults(live []jackett.Result, cached []history.CachedResult, e *resultEnricher) []searchResult {
	seen := make(map[string]bool)
	out := make([]searchResult, 0, len(live)+len(cached))

	for _, r := range live {
		out = append(out, e.enrich(r, false))
		if r.InfoHash != "" {
			seen[r.InfoHash] = true
		}
	}

	for _, r := range cached {
		if r.InfoHash != "" && seen[r.InfoHash] {
			continue
		}
		out = append(out, e.enrich(r.Result, true))
		if r.InfoHash != "" {
			seen[r.InfoHash] = true
		}
	}

	return out
}

// --- prefer-dual ranking + dual-only filter (docs/PLAN_PTBR_SUBS_DUAL.md D2/D5) ---

// dualEmitOpts carries the prefer_dual/dual_only query options shared by the
// plain and the SSE search endpoints. Both default to off.
type dualEmitOpts struct {
	preferDual bool
	dualOnly   bool
}

// parseDualOpts reads the prefer_dual/dual_only query params — "1"/"true" via
// queryBool, the same convention as ?all=.
func parseDualOpts(c *gin.Context) dualEmitOpts {
	return dualEmitOpts{
		preferDual: queryBool(c, "prefer_dual"),
		dualOnly:   queryBool(c, "dual_only"),
	}
}

// isDualAudio reports whether a result's audio flags mark it as PT-BR
// dual/dubbed audio — the predicate behind both the prefer_dual boost and the
// dual_only filter (Dual || Dub || PTBR).
func isDualAudio(flags parser.AudioFlags) bool {
	return flags.Dual || flags.Dub || flags.PTBR
}

// resolutionRank ranks a normalized resolution for the prefer_dual sort:
// 2160p/4K > 1080p > 720p > 480p; anything unparsed ranks lowest.
func resolutionRank(resolution string) int {
	switch resolution {
	case "2160p":
		return 4
	case "1080p":
		return 3
	case "720p":
		return 2
	case "480p":
		return 1
	}
	return 0
}

// sourceRank ranks a normalized source for the prefer_dual sort, following the
// locked ladder BluRay/Remux > WEB-DL > WEBRip > HDTV > DVDRip > CAM/TS.
// Sources the parser can emit but the ladder doesn't list are grouped with
// their nearest tier (HDRip → WEBRip, DVDScr → DVDRip, Workprint → CAM/TS);
// anything unparseable ranks below every known source.
func sourceRank(source string) int {
	switch source {
	case "BluRay":
		return 5
	case "WEB-DL":
		return 4
	case "WEBRip", "HDRip":
		return 3
	case "HDTV":
		return 2
	case "DVDRip", "DVDScr":
		return 1
	case "CAM", "TS", "Workprint":
		return 0
	}
	return -1
}

// dualSortKey is the prefer_dual sort key. Compared field by field in
// preferDualLess; ties keep their original relative order (stable sort).
type dualSortKey struct {
	resolution int
	source     int
	dual       bool
}

// dualSortKeyFor derives the key from a parsed quality + audio flags. A Remux
// release promotes to the BluRay source tier (the ladder's top tier is
// "BluRay/Remux").
func dualSortKeyFor(q parser.Quality, flags parser.AudioFlags) dualSortKey {
	src := sourceRank(q.Source)
	if q.Remux {
		src = max(src, sourceRank("BluRay"))
	}
	return dualSortKey{
		resolution: resolutionRank(q.Resolution),
		source:     src,
		dual:       isDualAudio(flags),
	}
}

// preferDualLess reports whether a sorts strictly before b under prefer_dual:
// quality tier first (resolution desc, then source desc) — a dual release
// never outranks a higher quality tier — and only then the dual flag, within
// the same tier. Feed it to sort.SliceStable so equal keys keep the original
// relative order (codec/seeders and every other secondary signal stay
// untouched within a group).
func preferDualLess(a, b dualSortKey) bool {
	if a.resolution != b.resolution {
		return a.resolution > b.resolution
	}
	if a.source != b.source {
		return a.source > b.source
	}
	return a.dual && !b.dual
}

// sortPreferDual stable-sorts results by preferDualLess, computing each
// element's key once up front so keys travel with their element through the
// sort (indexing a key slice by live sort position would compare the wrong
// pairs).
func sortPreferDual[E any](results []E, keyOf func(E) dualSortKey) {
	type keyed[E any] struct {
		value E
		key   dualSortKey
	}
	items := make([]keyed[E], len(results))
	for i, r := range results {
		items[i] = keyed[E]{value: r, key: keyOf(r)}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return preferDualLess(items[i].key, items[j].key)
	})
	for i, it := range items {
		results[i] = it.value
	}
}

// searchResultDualKey adapts an enriched result to the prefer_dual key.
func searchResultDualKey(r searchResult) dualSortKey {
	return dualSortKeyFor(r.Quality, r.AudioFlags)
}

// jackettResultDualKey adapts a raw Jackett result to the prefer_dual key
// (used by the SSE handler, which sorts before enrichment).
func jackettResultDualKey(r jackett.Result) dualSortKey {
	return dualSortKeyFor(parser.Parse(r.Title), parser.DetectAudioFlags(r.Title))
}

// cachedResultDualKey adapts a cached history row to the prefer_dual key.
func cachedResultDualKey(r history.CachedResult) dualSortKey {
	return jackettResultDualKey(r.Result)
}

// filterDualOnly keeps only PT-BR dual/dubbed audio results (Dual || Dub ||
// PTBR). It filters the response payload ONLY: callers keep saving/caching the
// full set, so history and dedup bookkeeping are unaffected.
func filterDualOnly(results []searchResult) []searchResult {
	kept := make([]searchResult, 0, len(results))
	for _, r := range results {
		if isDualAudio(r.AudioFlags) {
			kept = append(kept, r)
		}
	}
	return kept
}

// GetIndexers handles GET /api/indexers
func GetIndexers(client *jackett.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		indexers, err := client.GetIndexers()
		if err != nil {
			httpshared.RespondError(c, http.StatusBadGateway, err)
			return
		}
		c.JSON(http.StatusOK, indexers)
	}
}
