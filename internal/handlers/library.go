package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/library"
)

// LibraryList handles GET /api/library — user's playback history (most recent first).
// Admin with ?all=1 sees everyone's entries.
func LibraryList(lib *library.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && c.Query("all") == "1"
		limit := 0
		if l := c.Query("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		list, err := lib.List(userID, includeAll, limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, list)
	}
}

// LibraryGet handles GET /api/library/:id
func LibraryGet(lib *library.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin
		entry, err := lib.GetByID(id, userID, includeAll)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if entry == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": ErrNotFound})
			return
		}
		c.JSON(http.StatusOK, entry)
	}
}

// LibraryUpdateResume handles PATCH /api/library/:id with body {resumeSeconds, durationSeconds}.
// Called periodically by the player to persist playback position.
func LibraryUpdateResume(lib *library.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		var req struct {
			ResumeSeconds   float64 `json:"resumeSeconds"`
			DurationSeconds float64 `json:"durationSeconds"`
			FileIndex       *int    `json:"fileIndex"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// Pointer so an omitted fileIndex stays -1 (don't touch the column).
		fileIndex := -1
		if req.FileIndex != nil {
			fileIndex = *req.FileIndex
		}
		// Incognito entries still track resume progress — the entry is already
		// flagged incognito=1 and excluded from normal listings; saving position
		// allows the user to resume within their incognito session.
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		if err := lib.UpdateResume(id, userID, req.ResumeSeconds, req.DurationSeconds, fileIndex, isAdmin); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "saved"})
	}
}

// LibraryDeleteAll handles DELETE /api/library — clears the caller's whole
// Continue-Watching list. Honors ?all=1 for admins to wipe across users.
func LibraryDeleteAll(lib *library.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && c.Query("all") == "1"
		n, err := lib.DeleteAll(userID, includeAll)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": n})
	}
}

// LibraryDelete handles DELETE /api/library/:id
func LibraryDelete(lib *library.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		if err := lib.Delete(id, userID, isAdmin); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "deleted"})
	}
}
