package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// favoritesBatchMax caps names per favorites batch endpoint (Perf #9).
const favoritesBatchMax = 500

// FavoritesBatchRemove handles POST /api/stream/favorites/batch/remove
// {names:[]} → {affected,total,failed} — removes MANY favorites in ONE call so
// multi-select delete on FavoritesPage does not fire N DELETE /stream/favorite/:name
// (Perf #9). Empty/whitespace names land in failed; store errors too. Auth is
// user-scoped (same as the singular DELETE); admin ?all=1 expands scope.
func FavoritesBatchRemove(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		favs := s.Favorites()
		if favs == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": streamer.ErrFavoritesUnavail})
			return
		}
		var req struct {
			Names []string `json:"names"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || len(req.Names) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "names is required"})
			return
		}
		if len(req.Names) > favoritesBatchMax {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "too many names"})
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && queryBool(c, "all")
		affected, failed := batchRemoveFavorites(favs, req.Names, userID, includeAll)
		c.JSON(http.StatusOK, gin.H{
			"affected": affected,
			"total":    len(req.Names),
			"failed":   failed,
		})
	}
}

func batchRemoveFavorites(favs *streamer.FavoritesStore, names []string, userID int, includeAll bool) (int, []string) {
	seen := make(map[string]struct{}, len(names))
	affected := 0
	failed := make([]string, 0)
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			failed = append(failed, raw)
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if err := favs.Remove(name, userID, includeAll); err != nil {
			failed = append(failed, name)
			continue
		}
		affected++
	}
	return affected, failed
}

// FavoritesBatchSetFolder handles POST /api/stream/favorites/batch/folder
// {names:[], folderId?, toRoot?} → {affected,total,failed} — moves MANY
// favorites into a folder (or root) in ONE call so multi-select move does not
// fire N PATCH /stream/favorite/:name/folder (Perf #9). Same toRoot semantics
// as the singular move endpoint.
func FavoritesBatchSetFolder(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		favs := s.Favorites()
		if favs == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": streamer.ErrFavoritesUnavail})
			return
		}
		var req struct {
			Names    []string `json:"names"`
			FolderID *int     `json:"folderId"`
			ToRoot   bool     `json:"toRoot"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || len(req.Names) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "names is required"})
			return
		}
		if len(req.Names) > favoritesBatchMax {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "too many names"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		var folder *int
		if !req.ToRoot {
			folder = req.FolderID
		}
		affected, failed := batchSetFavoriteFolder(favs, req.Names, userID, folder)
		c.JSON(http.StatusOK, gin.H{
			"affected": affected,
			"total":    len(req.Names),
			"failed":   failed,
		})
	}
}

func batchSetFavoriteFolder(favs *streamer.FavoritesStore, names []string, userID int, folder *int) (int, []string) {
	seen := make(map[string]struct{}, len(names))
	affected := 0
	failed := make([]string, 0)
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			failed = append(failed, raw)
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if err := favs.MoveFavoriteToFolder(userID, name, folder); err != nil {
			failed = append(failed, name)
			continue
		}
		affected++
	}
	return affected, failed
}
