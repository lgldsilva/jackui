package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/history"
	"github.com/luizg/jackui/internal/jackett"
	"github.com/luizg/jackui/internal/middleware"
	"github.com/luizg/jackui/internal/parser"
	"github.com/luizg/jackui/internal/streamer"
)

// searchResult é o que o /api/search devolve por item. Estende
// jackett.Result com enriquecimentos calculados do lado do servidor:
//
//   - Cached: veio do cache local em vez de uma query live ao Jackett
//   - Quality: parse de release (resolução, codec, source, áudio)
//   - Playable / MediaKind: heurística "isso é audio/video tocável" (antes
//     vivia em web/src/lib/playable.ts; movido pra cá pra fonte única)
//   - IsFavorited / IsDownloaded: joins baratos contra favorites/downloads
//     do usuário, eliminando o ResultCard ter que manter Sets module-scope
type searchResult struct {
	jackett.Result
	Cached       bool            `json:"cached"`
	Quality      parser.Quality  `json:"quality"`
	Playable     bool            `json:"playable"`
	MediaKind    parser.MediaKind `json:"mediaKind"`
	IsFavorited  bool            `json:"isFavorited"`
	IsDownloaded bool            `json:"isDownloaded"`
}

// resultEnricher pré-carrega os sets de favorites/downloads do usuário uma
// vez e enriquece N results sem N queries. Construído por buildEnricher().
type resultEnricher struct {
	favHashes map[string]bool
	dlHashes  map[string]bool
}

func buildEnricher(favs *streamer.FavoritesStore, dls *downloads.Store, userID int, includeAll bool) *resultEnricher {
	e := &resultEnricher{
		favHashes: map[string]bool{},
		dlHashes:  map[string]bool{},
	}
	if favs != nil {
		if m, err := favs.HashSetForUser(userID, includeAll); err == nil {
			e.favHashes = m
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
		Result:    r,
		Cached:    cached,
		Quality:   q,
		Playable:  parser.IsPlayable(r.Title, r.CategoryID, r.MagnetURI, q.Resolution),
		MediaKind: parser.DetectKind(r.Title, r.CategoryID),
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
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrQueryRequired})
			return
		}
		indexers := parseIndexers(c)
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && c.Query("all") == "1"
		enricher := buildEnricher(favs, dls, userID, includeAll)

		liveResults, liveErr := client.Search(query, c.Query("category"), indexers)
		saveHistory(store, query, liveResults, userID, liveErr, c)
		merged := searchMerged(store, query, userID, includeAll, liveResults, enricher)

		if liveErr != nil && len(merged) == 0 {
			c.JSON(http.StatusBadGateway, gin.H{"error": liveErr.Error()})
			return
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

// GetIndexers handles GET /api/indexers
func GetIndexers(client *jackett.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		indexers, err := client.GetIndexers()
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, indexers)
	}
}
