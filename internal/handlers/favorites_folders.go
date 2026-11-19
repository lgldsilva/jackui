package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// Folder CRUD handlers. Routes are mounted under /api/stream/favorites/folders
// to keep them adjacent to the existing favorites endpoints. All operations
// are per-user — no admin override path yet (favorites tree is private).

func FoldersList(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		fs := s.Favorites()
		if fs == nil {
			httpshared.RespondErrorMessage(c, http.StatusServiceUnavailable, streamer.ErrFavoritesUnavail)
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		// The global reveal curtain (X-JackUI-Reveal-Hidden, the easter egg) or the
		// legacy ?includeHidden=1 reveal hidden folders.
		folders, err := fs.ListFolders(c.Request.Context(), userID, middleware.IsRevealHidden(c) || c.Query("includeHidden") == "1")
		if err != nil {
			if httpshared.AbortIfClientGone(c, err) {
				return
			}
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, folders)
	}
}

type folderBody struct {
	Name     string `json:"name"`
	ParentID *int   `json:"parentId"`
	Hidden   bool   `json:"hidden"`
}

func FolderCreate(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		fs := s.Favorites()
		if fs == nil {
			httpshared.RespondErrorMessage(c, http.StatusServiceUnavailable, streamer.ErrFavoritesUnavail)
			return
		}
		var body folderBody
		if err := c.ShouldBindJSON(&body); err != nil || body.Name == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "name required")
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		wctx, cancel := httpshared.DetachedCtx(c.Request.Context())
		defer cancel()
		f, err := fs.CreateFolder(wctx, userID, body.Name, body.ParentID, body.Hidden)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusCreated, f)
	}
}

type folderPatchBody struct {
	Name         *string `json:"name"`
	ParentID     *int    `json:"parentId"`
	ParentToRoot bool    `json:"parentToRoot"`
	Hidden       *bool   `json:"hidden"`
}

func FolderPatch(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		folderPatchHandler(c, s)
	}
}

func folderPatchHandler(c *gin.Context, s *streamer.Streamer) {
	fs := s.Favorites()
	if fs == nil {
		httpshared.RespondErrorMessage(c, http.StatusServiceUnavailable, streamer.ErrFavoritesUnavail)
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
		return
	}
	var body folderPatchBody
	if err := c.ShouldBindJSON(&body); err != nil {
		httpshared.RespondError(c, http.StatusBadRequest, err)
		return
	}
	userID, _, _ := auth.UserIDFromCtx(c)
	// Rename + move + hide are applied as one detached block so a disconnect
	// cannot leave the folder renamed but still visible/unmoved.
	wctx, cancel := httpshared.DetachedCtx(c.Request.Context())
	defer cancel()
	if err := applyFolderPatch(wctx, fs, folderKey{userID, id}, &body); err != nil {
		httpshared.RespondError(c, http.StatusInternalServerError, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// folderKey identifies one user's folder (keeps applyFolderPatch within 4 params).
type folderKey struct{ userID, id int }

func applyFolderPatch(ctx context.Context, fs *streamer.FavoritesStore, k folderKey, body *folderPatchBody) error {
	userID, id := k.userID, k.id
	if body.Name != nil && *body.Name != "" {
		if err := fs.RenameFolder(ctx, userID, id, *body.Name); err != nil {
			return err
		}
	}
	if body.ParentID != nil || body.ParentToRoot {
		var newParent *int
		if !body.ParentToRoot {
			newParent = body.ParentID
		}
		if err := fs.MoveFolder(ctx, userID, id, newParent); err != nil {
			return err
		}
	}
	if body.Hidden != nil {
		if err := fs.SetFolderHidden(ctx, userID, id, *body.Hidden); err != nil {
			return err
		}
	}
	return nil
}

func FolderDelete(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		fs := s.Favorites()
		if fs == nil {
			httpshared.RespondErrorMessage(c, http.StatusServiceUnavailable, streamer.ErrFavoritesUnavail)
			return
		}
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		wctx, cancel := httpshared.DetachedCtx(c.Request.Context())
		defer cancel()
		if err := fs.DeleteFolder(wctx, userID, id); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func FavoriteMoveToFolder(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		fs := s.Favorites()
		if fs == nil {
			httpshared.RespondErrorMessage(c, http.StatusServiceUnavailable, streamer.ErrFavoritesUnavail)
			return
		}
		name := c.Param("name")
		var body struct {
			FolderID *int `json:"folderId"`
			ToRoot   bool `json:"toRoot"` // explicit null move (vs unset)
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		var folder *int
		if !body.ToRoot {
			folder = body.FolderID
		}
		wctx, cancel := httpshared.DetachedCtx(c.Request.Context())
		defer cancel()
		if err := fs.MoveFavoriteToFolder(wctx, userID, name, folder); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}
