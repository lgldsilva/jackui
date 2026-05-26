package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/tmdb"
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
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "tmdb disabled"})
			return
		}
		m, err := c.Match(ctx.Request.Context(), title)
		if err != nil {
			if errors.Is(err, tmdb.ErrDisabled) {
				ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "tmdb disabled"})
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
