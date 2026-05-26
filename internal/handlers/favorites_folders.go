package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/streamer"
)

// Folder CRUD handlers. Routes are mounted under /api/stream/favorites/folders
// to keep them adjacent to the existing favorites endpoints. All operations
// are per-user — no admin override path yet (favorites tree is private).

func FoldersList(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		fs := s.Favorites()
		if fs == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "favorites store unavailable"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		folders, err := fs.ListFolders(userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, folders)
	}
}

type folderBody struct {
	Name     string `json:"name"`
	ParentID *int   `json:"parentId"`
}

func FolderCreate(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		fs := s.Favorites()
		if fs == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "favorites store unavailable"})
			return
		}
		var body folderBody
		if err := c.ShouldBindJSON(&body); err != nil || body.Name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name required"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		f, err := fs.CreateFolder(userID, body.Name, body.ParentID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, f)
	}
}

func FolderPatch(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		fs := s.Favorites()
		if fs == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "favorites store unavailable"})
			return
		}
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		var body struct {
			Name           *string `json:"name"`
			ParentID       *int    `json:"parentId"`
			ParentToRoot   bool    `json:"parentToRoot"` // explicit "set parent to null" — distinguished from "leave alone"
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if body.Name != nil && *body.Name != "" {
			if err := fs.RenameFolder(userID, id, *body.Name); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		if body.ParentID != nil || body.ParentToRoot {
			var newParent *int
			if !body.ParentToRoot {
				newParent = body.ParentID
			}
			if err := fs.MoveFolder(userID, id, newParent); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		c.Status(http.StatusNoContent)
	}
}

func FolderDelete(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		fs := s.Favorites()
		if fs == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "favorites store unavailable"})
			return
		}
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if err := fs.DeleteFolder(userID, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func FavoriteMoveToFolder(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		fs := s.Favorites()
		if fs == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "favorites store unavailable"})
			return
		}
		name := c.Param("name")
		var body struct {
			FolderID    *int `json:"folderId"`
			ToRoot      bool `json:"toRoot"` // explicit null move (vs unset)
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		var folder *int
		if !body.ToRoot {
			folder = body.FolderID
		}
		if err := fs.MoveFavoriteToFolder(userID, name, folder); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}
