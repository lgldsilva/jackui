package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/history"
	"github.com/luizg/jackui/internal/jackett"
	"github.com/luizg/jackui/internal/parser"
)

type searchResult struct {
	jackett.Result
	Cached  bool            `json:"cached"`
	Quality parser.Quality  `json:"quality"`
}

func enriched(r jackett.Result, cached bool) searchResult {
	return searchResult{
		Result:  r,
		Cached:  cached,
		Quality: parser.Parse(r.Title),
	}
}

// Search handles GET /api/search?q=&indexers=&category=
// Merges live Jackett results with local cache; remote results take priority on dedup.
func Search(client *jackett.Client, store *history.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := c.Query("q")
		if query == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'q' is required"})
			return
		}

		category := c.Query("category")

		var indexers []string
		if ip := c.Query("indexers"); ip != "" {
			for _, idx := range strings.Split(ip, ",") {
				if idx = strings.TrimSpace(idx); idx != "" {
					indexers = append(indexers, idx)
				}
			}
		}

		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && c.Query("all") == "1"

		liveResults, liveErr := client.Search(query, category, indexers)

		if liveErr == nil && store != nil && len(liveResults) > 0 {
			go store.Save(query, liveResults, userID)
		}

		var cached []history.CachedResult
		if store != nil {
			cached, _ = store.Search(query, userID, includeAll)
		}

		merged := mergeResults(liveResults, cached)

		if liveErr != nil && len(merged) == 0 {
			c.JSON(http.StatusBadGateway, gin.H{"error": liveErr.Error()})
			return
		}

		c.JSON(http.StatusOK, merged)
	}
}

// mergeResults combines live and cached results.
// Live results are added first; cached results only appear if their infoHash is not already present.
func mergeResults(live []jackett.Result, cached []history.CachedResult) []searchResult {
	seen := make(map[string]bool)
	out := make([]searchResult, 0, len(live)+len(cached))

	for _, r := range live {
		out = append(out, enriched(r, false))
		if r.InfoHash != "" {
			seen[r.InfoHash] = true
		}
	}

	for _, r := range cached {
		if r.InfoHash != "" && seen[r.InfoHash] {
			continue
		}
		out = append(out, enriched(r.Result, true))
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
