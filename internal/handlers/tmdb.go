package handlers

import (
	"errors"
	"net/http"
	"strconv"

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
