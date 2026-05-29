package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/history"
	"github.com/luizg/jackui/internal/parser"
	"github.com/luizg/jackui/internal/streamer"
)

// enrichedCached is a cached result enriched with parsed quality, playable
// heuristic, media kind, and user-specific flags (favorited/downloaded).
// Origin query is preserved via embedded CachedResult.Query for UI badges.
type enrichedCached struct {
	history.CachedResult
	Quality      parser.Quality   `json:"quality"`
	Playable     bool             `json:"playable"`
	MediaKind    parser.MediaKind `json:"mediaKind"`
	IsFavorited  bool             `json:"isFavorited"`
	IsDownloaded bool             `json:"isDownloaded"`
}

func enrichCached(items []history.CachedResult, e *resultEnricher) []enrichedCached {
	out := make([]enrichedCached, len(items))
	for i, r := range items {
		q := parser.Parse(r.Title)
		row := enrichedCached{
			CachedResult: r,
			Quality:      q,
			Playable:     parser.IsPlayable(r.Title, r.CategoryID, r.MagnetURI, q.Resolution),
			MediaKind:    parser.DetectKind(r.Title, r.CategoryID),
		}
		if e != nil && r.InfoHash != "" {
			row.IsFavorited = e.favHashes[r.InfoHash]
			row.IsDownloaded = e.dlHashes[r.InfoHash]
		}
		out[i] = row
	}
	return out
}

// GetHistory handles GET /api/history — recent search entries (filtered per-user; admin sees all).
// ?all=1 lets admin opt-in to global view; default is own data even for admin.
func GetHistory(store *history.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && c.Query("all") == "1"
		entries, err := store.RecentEntries(100, userID, includeAll)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if entries == nil {
			entries = []history.Entry{}
		}
		c.JSON(http.StatusOK, entries)
	}
}

// GetHistoryResults handles GET /api/history/results?q= — returns cached results for a query.
func GetHistoryResults(store *history.Store, favs *streamer.FavoritesStore, dls *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := c.Query("q")
		if query == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrQueryRequired})
			return
		}

		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && c.Query("all") == "1"
		results, err := store.Search(query, userID, includeAll)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		e := buildEnricher(favs, dls, userID, includeAll)
		c.JSON(http.StatusOK, enrichCached(results, e))
	}
}

// SearchCache handles GET /api/history/cache?q=&limit= — FTS5 search across all cached results.
func SearchCache(store *history.Store, favs *streamer.FavoritesStore, dls *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := c.Query("q")
		if query == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrQueryRequired})
			return
		}
		limit := 200
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && c.Query("all") == "1"
		results, err := store.SearchAll(query, limit, userID, includeAll)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		e := buildEnricher(favs, dls, userID, includeAll)
		c.JSON(http.StatusOK, enrichCached(results, e))
	}
}

// DeleteHistory handles DELETE /api/history — clears the user's results, or one query if ?q= is provided.
// Admins with ?all=1 wipe everyone's data.
func DeleteHistory(store *history.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && c.Query("all") == "1"
		if q := c.Query("q"); q != "" {
			if err := store.DeleteQuery(q, userID, includeAll); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"message": "query cleared"})
			return
		}
		if err := store.DeleteAll(userID, includeAll); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "history cleared"})
	}
}
