package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/playlists"
)

// PlaylistsList handles GET /api/playlists
func PlaylistsList(store *playlists.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && c.Query("all") == "1"
		list, err := store.List(userID, includeAll)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, list)
	}
}

// PlaylistsCreate handles POST /api/playlists — {name, description}
func PlaylistsCreate(store *playlists.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrNameRequired})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		p, err := store.Create(userID, req.Name, req.Description)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, p)
	}
}

// PlaylistsGet handles GET /api/playlists/:id — returns playlist + items
func PlaylistsGet(store *playlists.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		p, err := store.Get(id, userID, isAdmin)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if p == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": ErrNotFound})
			return
		}
		items, _ := store.Items(id, userID, isAdmin)
		c.JSON(http.StatusOK, gin.H{"playlist": p, "items": items})
	}
}

// PlaylistsUpdate handles PATCH /api/playlists/:id — {name, description}
func PlaylistsUpdate(store *playlists.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		if err := store.Update(id, userID, req.Name, req.Description, isAdmin); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "updated"})
	}
}

// PlaylistsDelete handles DELETE /api/playlists/:id
func PlaylistsDelete(store *playlists.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		if err := store.Delete(id, userID, isAdmin); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "deleted"})
	}
}

// PlaylistsAddItem handles POST /api/playlists/:id/items — {title, magnet, infoHash, fileIndex, libraryId}
func PlaylistsAddItem(store *playlists.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		var req playlists.Item
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		it, err := store.AddItem(id, userID, req, isAdmin)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, it)
	}
}

// PlaylistsRemoveItem handles DELETE /api/playlists/:id/items/:itemId
func PlaylistsRemoveItem(store *playlists.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		itemID, _ := strconv.Atoi(c.Param("itemId"))
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		if err := store.RemoveItem(id, itemID, userID, isAdmin); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "removed"})
	}
}

// PlaylistsReorderItem handles PATCH /api/playlists/:id/items/:itemId — {position}
func PlaylistsReorderItem(store *playlists.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		itemID, _ := strconv.Atoi(c.Param("itemId"))
		var req struct {
			Position int `json:"position"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		if err := store.Reorder(id, itemID, userID, req.Position, isAdmin); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "reordered"})
	}
}
