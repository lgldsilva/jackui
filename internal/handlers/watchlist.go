package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/watchlist"
)

type watchlistInput struct {
	Query        string `json:"query"`
	Category     string `json:"category"`
	MinSeeders   int    `json:"minSeeders"`
	NtfyTopic    string `json:"ntfyTopic"`
	SchedKind    string `json:"schedKind"`    // interval | daily | weekly (empty → interval)
	SchedMinutes int    `json:"schedMinutes"` // interval: every N minutes (<= 0 → server default)
	SchedWeekday int    `json:"schedWeekday"` // weekly: 0=Sunday … 6=Saturday
	SchedHour    int    `json:"schedHour"`    // daily/weekly
	SchedMinute  int    `json:"schedMinute"`  // daily/weekly
}

func (in watchlistInput) schedule() watchlist.Schedule {
	return watchlist.Schedule{
		Kind:    in.SchedKind,
		Minutes: in.SchedMinutes,
		Weekday: in.SchedWeekday,
		Hour:    in.SchedHour,
		Minute:  in.SchedMinute,
	}
}

// WatchlistKicker triggers an immediate background check of one watchlist —
// implemented by *watchlist.Worker (nil-safe by design).
type WatchlistKicker interface {
	Kick(id int)
}

// WatchlistList — GET /api/watchlists
func WatchlistList(s *watchlist.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		list, err := s.List(userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, list)
	}
}

// WatchlistCreate — POST /api/watchlists. On success the worker is kicked so
// the first check runs right away instead of waiting for the schedule.
func WatchlistCreate(s *watchlist.Store, k WatchlistKicker) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		var in watchlistInput
		if err := c.BindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		w, err := s.Create(userID, in.Query, in.Category, in.MinSeeders, in.NtfyTopic, in.schedule())
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if k != nil {
			k.Kick(w.ID)
		}
		c.JSON(http.StatusOK, w)
	}
}

// WatchlistUpdate — PUT /api/watchlists/:id
func WatchlistUpdate(s *watchlist.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		var in watchlistInput
		if err := c.BindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := s.Update(userID, id, in.Query, in.Category, in.MinSeeders, in.NtfyTopic, in.schedule()); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// WatchlistDelete — DELETE /api/watchlists/:id
func WatchlistDelete(s *watchlist.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		if err := s.Delete(userID, id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	}
}

// WatchlistHits — GET /api/watchlists/:id/hits
func WatchlistHits(s *watchlist.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		limit := 50
		if l := c.Query("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		hits, err := s.Hits(userID, id, limit)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, hits)
	}
}
