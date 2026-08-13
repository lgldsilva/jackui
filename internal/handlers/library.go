package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// LibraryList handles GET /api/library — user's playback history (most recent first).
// Admin with ?all=1 sees everyone's entries. Entries tied to a hidden favourite
// folder are dropped unless the request opened the curtain (X-JackUI-Reveal-Hidden).
func LibraryList(lib *library.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && queryBool(c, "all")
		limit := 0
		if l := c.Query("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		list, err := lib.List(userID, includeAll, limit)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		list = dropHiddenLibrary(list, hiddenHashSet(c, s, userID, includeAll))
		list = dropHiddenLocalLibrary(c, s, list, userID)
		c.JSON(http.StatusOK, list)
	}
}

// LibraryGetByHash handles GET /api/library/hash/:hash — O(1) lookup used by
// the player to restore resume position. Avoids the O(n) libraryList({limit:100})
// scan that silently missed titles past the first hundred.
//
// Same visibility as LibraryList: incognito rows stay out of the normal
// session, and hidden favourite / local-curtain hashes 404 (not 403) so a
// leaked infoHash cannot probe the curtain.
func LibraryGetByHash(lib *library.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		hash := strings.TrimSpace(c.Param("hash"))
		if hash == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		entry, err := lib.GetByHashPublic(userID, hash)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		if visibleLibraryEntry(c, s, userID, entry) == nil {
			httpshared.RespondErrorMessage(c, http.StatusNotFound, ErrNotFound)
			return
		}
		c.JSON(http.StatusOK, entry)
	}
}

// visibleLibraryEntry applies the same hidden-curtain filters as LibraryList
// (without ?all=1) to a single row. nil in or filtered out → nil.
//
// includeAll is always false: this helper is the player's own-user resume
// lookup. Passing isAdmin here used to union every user's hidden-folder
// hashes, so an admin 404'd their own resume when anyone else hid the torrent.
func visibleLibraryEntry(c *gin.Context, s *streamer.Streamer, userID int, entry *library.Entry) *library.Entry {
	if entry == nil {
		return nil
	}
	list := []library.Entry{*entry}
	list = dropHiddenLibrary(list, hiddenHashSet(c, s, userID, false))
	list = dropHiddenLocalLibrary(c, s, list, userID)
	if len(list) == 0 {
		return nil
	}
	return &list[0]
}

// LibraryGet handles GET /api/library/:id
func LibraryGet(lib *library.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin
		entry, err := lib.GetByID(id, userID, includeAll)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		if entry == nil {
			httpshared.RespondErrorMessage(c, http.StatusNotFound, ErrNotFound)
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
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		var req struct {
			ResumeSeconds   float64 `json:"resumeSeconds"`
			DurationSeconds float64 `json:"durationSeconds"`
			FileIndex       *int    `json:"fileIndex"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
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
			httpshared.RespondError(c, http.StatusInternalServerError, err)
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
		includeAll := isAdmin && queryBool(c, "all")
		n, err := lib.DeleteAll(userID, includeAll)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
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
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		if err := lib.Delete(id, userID, isAdmin); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "deleted"})
	}
}
