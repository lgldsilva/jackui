package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/stats"
	"github.com/lgldsilva/jackui/internal/watchlist"
)

// statsLibraryLimit caps how many library rows feed the aggregation — well
// above any realistic per-user library, just a guard against a runaway query.
const statsLibraryLimit = 5000

type downloadStats struct {
	Total           int   `json:"total"`
	Completed       int   `json:"completed"`
	BytesDownloaded int64 `json:"bytesDownloaded"`
}

type watchlistStats struct {
	Count int `json:"count"`
	Hits  int `json:"hits"`
}

type userStats struct {
	Library       stats.LibraryAgg `json:"library"`
	Downloads     downloadStats    `json:"downloads"`
	SearchQueries int              `json:"searchQueries"`
	Watchlists    watchlistStats   `json:"watchlists"`
}

// Stats — GET /api/stats. Personal usage aggregates for the logged-in user,
// computed live from the existing stores (no new data collection; incognito
// rows are excluded at the store level). Stores the instance doesn't have
// (nil) just contribute zeroes.
func Stats(lib *library.Store, dl *downloads.Store, hist *history.Store, wl *watchlist.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		out, err := buildUserStats(userID, lib, dl, hist, wl)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, out)
	}
}

// buildUserStats assembles the response from whichever stores exist — split
// out of the handler per section to keep each unit's cognitive load low.
func buildUserStats(userID int, lib *library.Store, dl *downloads.Store, hist *history.Store, wl *watchlist.Store) (userStats, error) {
	out := userStats{Library: stats.Aggregate(nil, time.Now(), time.Local)}
	if err := fillLibraryStats(&out, lib, userID); err != nil {
		return out, err
	}
	if err := fillDownloadStats(&out, dl, userID); err != nil {
		return out, err
	}
	fillActivityStats(&out, hist, wl, userID)
	return out, nil
}

func fillLibraryStats(out *userStats, lib *library.Store, userID int) error {
	if lib == nil {
		return nil
	}
	entries, err := lib.List(userID, false, statsLibraryLimit)
	if err != nil {
		return err
	}
	out.Library = stats.Aggregate(entries, time.Now(), time.Local)
	return nil
}

func fillDownloadStats(out *userStats, dl *downloads.Store, userID int) error {
	if dl == nil {
		return nil
	}
	total, completed, bytes, err := dl.UserStats(userID)
	if err != nil {
		return err
	}
	out.Downloads = downloadStats{Total: total, Completed: completed, BytesDownloaded: bytes}
	return nil
}

// fillActivityStats adds the best-effort blocks (search history + watchlists):
// failures here zero the field instead of failing the whole response.
func fillActivityStats(out *userStats, hist *history.Store, wl *watchlist.Store, userID int) {
	if hist != nil {
		if n, err := hist.DistinctQueryCount(userID); err == nil {
			out.SearchQueries = n
		}
	}
	if wl == nil {
		return
	}
	lists, err := wl.List(userID)
	if err != nil {
		return
	}
	out.Watchlists.Count = len(lists)
	for _, w := range lists {
		out.Watchlists.Hits += w.HitCount
	}
}
