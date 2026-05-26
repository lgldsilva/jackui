package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/downloads"
)

// DownloadsList handles GET /api/downloads — current user's queue.
func DownloadsList(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		list, err := store.List(userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, list)
	}
}

// DownloadsCreate handles POST /api/downloads — enqueues a new full-file
// download. Body: { infoHash, fileIndex, magnet, name, filePath, fileSize }
func DownloadsCreate(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			InfoHash  string `json:"infoHash"`
			FileIndex int    `json:"fileIndex"`
			Magnet    string `json:"magnet"`
			Name      string `json:"name"`
			FilePath  string `json:"filePath"`
			FileSize  int64  `json:"fileSize"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.InfoHash == "" || req.Magnet == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "infoHash and magnet are required"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		d, err := store.Create(downloads.Download{
			UserID:    userID,
			InfoHash:  req.InfoHash,
			FileIndex: req.FileIndex,
			FilePath:  req.FilePath,
			FileSize:  req.FileSize,
			Name:      req.Name,
			Magnet:    req.Magnet,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, d)
	}
}

// DownloadsDelete handles DELETE /api/downloads/:id — cancel + remove row.
func DownloadsDelete(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if err := store.Delete(userID, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// DownloadsPause handles PATCH /api/downloads/:id/pause — flips status to paused.
// The worker's next tick will untrack the row and unregister the streamer
// protection, but the on-disk bytes already fetched stay there.
func DownloadsPause(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		// Ownership check first
		if _, err := store.Get(userID, id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if err := store.SetStatus(id, downloads.StatusPaused); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// DownloadsResume handles PATCH /api/downloads/:id/resume — flips status to downloading.
// The worker picks it up on the next tick.
func DownloadsResume(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if _, err := store.Get(userID, id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if err := store.SetStatus(id, downloads.StatusDownloading); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}
