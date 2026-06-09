package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// hiddenLocalSet returns the set of paths (within mount) the user has hidden, or
// nil when the request opened the curtain / favourites is unavailable. Keyed by
// the user-facing (post StripUserScope) relative path, matching local.Entry.Path.
func hiddenLocalSet(c *gin.Context, s *streamer.Streamer, userID int, mount string) map[string]bool {
	if middleware.IsRevealHidden(c) || s == nil || s.Favorites() == nil {
		return nil
	}
	paths, err := s.Favorites().HiddenLocalPaths(userID)
	if err != nil || len(paths) == 0 {
		return nil
	}
	set := map[string]bool{}
	for _, p := range paths {
		if p.Mount == mount {
			set[p.Path] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// dropHiddenLocalEntries removes entries whose path the user has hidden. A
// nil/empty set returns the list untouched.
func dropHiddenLocalEntries(entries []local.Entry, hidden map[string]bool) []local.Entry {
	if len(hidden) == 0 {
		return entries
	}
	out := entries[:0]
	for _, e := range entries {
		if !hidden[e.Path] {
			out = append(out, e)
		}
	}
	return out
}

// LocalSetHidden handles POST /api/local/hidden — marks (or unmarks) a local
// (mount, path) as hidden for the current user. Body: {mount, path, hidden}.
func LocalSetHidden(b *local.Browser, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Mount  string `json:"mount"`
			Path   string `json:"path"`
			Hidden bool   `json:"hidden"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Mount == "" || req.Path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPathParam})
			return
		}
		if !checkMountAccess(b, c, req.Mount) {
			return
		}
		favs := s.Favorites()
		if favs == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": streamer.ErrFavoritesUnavail})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if err := favs.SetLocalPathHidden(userID, req.Mount, req.Path, req.Hidden); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"mount": req.Mount, "path": req.Path, "hidden": req.Hidden})
	}
}

// LocalListHidden handles GET /api/local/hidden — the user's hidden local paths,
// for the UI to mark/indicate them when the curtain is open.
func LocalListHidden(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		favs := s.Favorites()
		if favs == nil {
			c.JSON(http.StatusOK, []streamer.HiddenLocalPath{})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		paths, err := favs.HiddenLocalPaths(userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if paths == nil {
			paths = []streamer.HiddenLocalPath{}
		}
		c.JSON(http.StatusOK, paths)
	}
}
