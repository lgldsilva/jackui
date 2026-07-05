package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/tmdb"
)

// TmdbMatch — GET /api/tmdb/match?title=Inception+2010
// Returns 200+match, 204 (no match), or 503 (no key).
func TmdbMatch(c *tmdb.Client) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		title := ctx.Query("title")
		if title == "" {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "title is required"})
			return
		}
		if c == nil {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
			return
		}
		m, err := c.Match(ctx.Request.Context(), title)
		if err != nil {
			if errors.Is(err, tmdb.ErrDisabled) {
				ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
				return
			}
			ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if m == nil {
			ctx.Status(http.StatusNoContent)
			return
		}
		ctx.JSON(http.StatusOK, m)
	}
}

// TmdbMatchBatch — POST /api/tmdb/match/batch {titles:[...]} → {matches:{title:Match}}.
// Resolves MANY titles in ONE call (each c.Match is cached 30d server-side), so a
// grid of N cards seeds its posters with a single round-trip instead of N GET
// /tmdb/match (the frontend N+1). Titles with no match are simply absent from the
// map. Bounded concurrency keeps a cold first-search from firing N serial API hits.
func TmdbMatchBatch(c *tmdb.Client) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req struct {
			Titles []string `json:"titles"`
		}
		if err := ctx.ShouldBindJSON(&req); err != nil || len(req.Titles) == 0 {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "titles is required"})
			return
		}
		if c == nil {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
			return
		}
		if len(req.Titles) > 100 {
			ctx.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "too many titles"})
			return
		}
		matches := make(map[string]*tmdb.Match, len(req.Titles))
		var mu sync.Mutex
		sem := make(chan struct{}, 6)
		var wg sync.WaitGroup
		rctx := ctx.Request.Context()
		for _, t := range req.Titles {
			if t == "" {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(t string) {
				defer wg.Done()
				defer func() { <-sem }()
				m, err := c.Match(rctx, t)
				if err != nil || m == nil {
					return
				}
				mu.Lock()
				matches[t] = m
				mu.Unlock()
			}(t)
		}
		wg.Wait()
		ctx.JSON(http.StatusOK, gin.H{"matches": matches})
	}
}

// TmdbTrending — GET /api/tmdb/trending[?year=&genre=]. Without filters: this
// week's trending (with ↑/↓ direction). With year/genre: TMDB /discover.
// 200+list, 503 (no key), or 502 on upstream error.
func TmdbTrending(c *tmdb.Client) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if c == nil {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
			return
		}
		year, _ := strconv.Atoi(ctx.Query("year"))
		genre, _ := strconv.Atoi(ctx.Query("genre"))
		var items []tmdb.Match
		var err error
		if year > 0 || genre > 0 {
			items, err = c.Discover(ctx.Request.Context(), year, genre)
		} else {
			items, err = c.Trending(ctx.Request.Context())
		}
		if err != nil {
			if errors.Is(err, tmdb.ErrDisabled) {
				ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
				return
			}
			ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, items)
	}
}

// TmdbVideos — GET /api/tmdb/videos?kind=movie|tv&id=123. YouTube trailers for
// a title, best first. 200+list (possibly empty), 400 on bad params, 503 (no
// key), or 502 on upstream error.
func TmdbVideos(c *tmdb.Client) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		kind := ctx.Query("kind")
		id, _ := strconv.Atoi(ctx.Query("id"))
		if (kind != "movie" && kind != "tv") || id <= 0 {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "kind (movie|tv) and id are required"})
			return
		}
		if c == nil {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
			return
		}
		videos, err := c.Videos(ctx.Request.Context(), kind, id)
		if err != nil {
			if errors.Is(err, tmdb.ErrDisabled) {
				ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
				return
			}
			ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, videos)
	}
}

// TmdbGenres — GET /api/tmdb/genres. Merged movie+tv genre list for the Discover
// filter dropdown. 200+list, 503 (no key), or 502 on upstream error.
func TmdbGenres(c *tmdb.Client) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if c == nil {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
			return
		}
		genres, err := c.Genres(ctx.Request.Context())
		if err != nil {
			if errors.Is(err, tmdb.ErrDisabled) {
				ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrTMDBDisabled})
				return
			}
			ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, genres)
	}
}
