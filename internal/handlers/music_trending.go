package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/musictrending"
)

// MusicTrending handles GET /api/music/trending?country=&limit= — proxies
// Apple's keyless top-albums RSS (see internal/musictrending) for the Discover
// grid in Música mode. Country defaults to us; limit is clamped by the client.
func MusicTrending(mc *musictrending.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if mc == nil {
			httpshared.RespondErrorMessage(c, http.StatusServiceUnavailable, "music trending disabled")
			return
		}
		limit, _ := strconv.Atoi(c.Query("limit"))
		albums, err := mc.Top(c.Request.Context(), c.Query("country"), limit)
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadGateway, "upstream error")
			return
		}
		c.Header(httpshared.CacheControl, httpshared.CachePublicDay)
		c.JSON(http.StatusOK, gin.H{"albums": albums})
	}
}
