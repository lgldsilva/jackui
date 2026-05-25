package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/jackett"
)

// Search handles GET /api/search?q=&indexers=&category=
func Search(client *jackett.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := c.Query("q")
		if query == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'q' is required"})
			return
		}

		category := c.Query("category")

		indexersParam := c.Query("indexers")
		var indexers []string
		if indexersParam != "" {
			for _, idx := range strings.Split(indexersParam, ",") {
				idx = strings.TrimSpace(idx)
				if idx != "" {
					indexers = append(indexers, idx)
				}
			}
		}

		results, err := client.Search(query, category, indexers)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, results)
	}
}

// GetIndexers handles GET /api/indexers
func GetIndexers(client *jackett.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		indexers, err := client.GetIndexers()
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, indexers)
	}
}
