package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/lyrics"
)

// LyricsGet handles GET /api/lyrics?title=&artist=&album=&duration= — proxies
// LrcLib through the backend (see internal/lyrics for why not the browser).
// Lyrics are best-effort: a miss returns 200 with an empty body, not an error,
// so the player just hides the panel.
func LyricsGet(lc *lyrics.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if lc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "lyrics disabled"})
			return
		}
		title := strings.TrimSpace(c.Query("title"))
		if title == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title is required"})
			return
		}
		dur, _ := strconv.Atoi(c.Query("duration"))
		lyr, err := lc.Get(c.Request.Context(), c.Query("artist"), title, c.Query("album"), dur)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Header(CacheControl, CachePublicDay)
		c.JSON(http.StatusOK, lyr)
	}
}
