package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/history"
	"github.com/luizg/jackui/internal/jackett"
)

// refreshTTL is how long we keep a refreshed swarm count cached before allowing
// another Jackett poll for the same row. 5 minutes matches what most public
// trackers report as their internal scrape granularity — refreshing more often
// just hits the cache anyway and risks rate-limiting from Jackett.
const refreshTTL = 5 * time.Minute

// refreshCacheEntry is one cached swarm-count poll. Stored per history row ID
// (results.id), so two users opening the same title row don't blow each
// other's cache away.
type refreshCacheEntry struct {
	Seeders   int
	Leechers  int
	FetchedAt time.Time
}

// refreshCache is the in-memory TTL cache fronting Jackett. Keyed by
// results.id (int64). The mutex protects map mutation; a single-writer model
// is plenty given polling happens only when the user clicks the refresh button.
type refreshCache struct {
	mu sync.Mutex
	m  map[int64]refreshCacheEntry
}

func newRefreshCache() *refreshCache {
	return &refreshCache{m: make(map[int64]refreshCacheEntry)}
}

func (c *refreshCache) get(id int64) (refreshCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[id]
	if !ok {
		return refreshCacheEntry{}, false
	}
	if time.Since(e.FetchedAt) > refreshTTL {
		delete(c.m, id)
		return refreshCacheEntry{}, false
	}
	return e, true
}

func (c *refreshCache) set(id int64, e refreshCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[id] = e
}

// HistoryRefreshResponse is the JSON shape returned by the refresh endpoint.
type HistoryRefreshResponse struct {
	ID        int64     `json:"id"`
	Seeders   int       `json:"seeders"`
	Leechers  int       `json:"leechers"`
	FetchedAt time.Time `json:"fetchedAt"`
	Cached    bool      `json:"cached"` // true when this came from the TTL cache, not a fresh poll
}

// findFreshMatch picks the best match for the original row inside a list of
// fresh Jackett results. Priority order:
//
//  1. Exact infoHash match (rock-solid identity, doesn't depend on title spelling)
//  2. Exact title match (case-sensitive — public trackers rarely re-rename)
//  3. Case-insensitive title match (fallback for trackers that normalize casing)
//
// Returns nil when nothing matches; the caller treats that as "row vanished
// from Jackett" and surfaces zeros to the UI.
func findFreshMatch(orig *history.CachedResult, fresh []jackett.Result) *jackett.Result {
	if orig.InfoHash != "" {
		for i := range fresh {
			if strings.EqualFold(fresh[i].InfoHash, orig.InfoHash) {
				return &fresh[i]
			}
		}
	}
	// Exact title match
	for i := range fresh {
		if fresh[i].Title == orig.Title {
			return &fresh[i]
		}
	}
	// Case-insensitive title match
	for i := range fresh {
		if strings.EqualFold(fresh[i].Title, orig.Title) {
			return &fresh[i]
		}
	}
	return nil
}

// HistoryRefresh handles POST /api/history/:id/refresh — re-polls Jackett for
// the original title and returns the latest seeders/leechers. Per-row TTL
// cache (5min) avoids hammering Jackett when the user spams the button across
// many rows.
//
// Response body (200): {id, seeders, leechers, fetchedAt, cached}
//   - cached=true means the response is from the in-memory TTL cache and no
//     new Jackett call was made.
//   - 404 when the row doesn't exist or the user doesn't own it.
//   - 502 when Jackett is unreachable AND no cached value is available.
func HistoryRefresh(store *history.Store, jck *jackett.Client) gin.HandlerFunc {
	cache := newRefreshCache()
	return func(c *gin.Context) {
		historyRefreshHandler(c, store, jck, cache)
	}
}

func historyRefreshHandler(c *gin.Context, store *history.Store, jck *jackett.Client, cache *refreshCache) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
		return
	}

	userID, isAdmin, _ := auth.UserIDFromCtx(c)
	row, err := store.GetResult(id, userID, isAdmin)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "history row not found"})
		return
	}

	if tryServeCachedRefresh(c, id, cache) {
		return
	}

	if jck == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Jackett client not configured"})
		return
	}

	queryStr, qerr := refreshQueryStr(row)
	if qerr != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": qerr.Error()})
		return
	}
	fresh, err := jck.Search(queryStr, "", nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "jackett search failed: " + err.Error()})
		return
	}

	match := findFreshMatch(row, fresh)
	seeders, leechers := seedersFromMatch(match)

	now := time.Now()
	_ = store.UpdateSeedersLeechers(id, seeders, leechers)
	cache.set(id, refreshCacheEntry{
		Seeders:   seeders,
		Leechers:  leechers,
		FetchedAt: now,
	})

	c.JSON(http.StatusOK, HistoryRefreshResponse{
		ID:        id,
		Seeders:   seeders,
		Leechers:  leechers,
		FetchedAt: now,
		Cached:    false,
	})
}

func tryServeCachedRefresh(c *gin.Context, id int64, cache *refreshCache) bool {
	if cached, ok := cache.get(id); ok {
		c.JSON(http.StatusOK, HistoryRefreshResponse{
			ID:        id,
			Seeders:   cached.Seeders,
			Leechers:  cached.Leechers,
			FetchedAt: cached.FetchedAt,
			Cached:    true,
		})
		return true
	}
	return false
}

func refreshQueryStr(row *history.CachedResult) (string, error) {
	if row.Title != "" {
		return row.Title, nil
	}
	if row.Query != "" {
		return row.Query, nil
	}
	return "", fmt.Errorf("no title to search")
}

func seedersFromMatch(match *jackett.Result) (int, int) {
	if match != nil {
		return match.Seeders, match.Leechers
	}
	return 0, 0
}
