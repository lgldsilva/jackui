package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/musictrending"
)

// MusicTrending handles GET /api/music/trending?country=&limit= — proxies
// Apple's keyless top-albums RSS (see internal/musictrending) for the Discover
// grid in Música mode. Country defaults to us; limit is clamped by the client.
func MusicTrending(mc *musictrending.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if mc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "music trending disabled"})
			return
		}
		limit, _ := strconv.Atoi(c.Query("limit"))
		albums, err := mc.Top(c.Request.Context(), c.Query("country"), limit)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream error"})
			return
		}
		c.Header(CacheControl, CachePublicDay)
		c.JSON(http.StatusOK, gin.H{"albums": albums})
	}
}
