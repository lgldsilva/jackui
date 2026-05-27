package handlers

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/local"
)

// LocalMounts handles GET /api/local/mounts -> []Mount
func LocalMounts(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, b.Mounts())
	}
}

// LocalList handles GET /api/local/list?mount=NAME&path=REL -> []Entry
func LocalList(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount parameter"})
			return
		}

		entries, err := b.List(mount, path)
		if err != nil {
			if isTraversalErr(err) {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "path not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, entries)
	}
}

// LocalFile handles GET /api/local/file?mount=NAME&path=REL/FILE
// Uses http.ServeFile which handles Range requests natively.
func LocalFile(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount or path parameter"})
			return
		}

		abs, err := b.ResolvePath(mount, path)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		stat, err := os.Stat(abs)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if stat.IsDir() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "path is a directory"})
			return
		}

		http.ServeFile(c.Writer, c.Request, abs)
	}
}

func isTraversalErr(err error) bool {
	s := err.Error()
	return strings.Contains(s, "traversal") ||
		strings.Contains(s, "must be relative") ||
		strings.Contains(s, "mount") && strings.Contains(s, "not found")
}
