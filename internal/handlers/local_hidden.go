package handlers

import (
	"net/http"
	"strings"

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

// localPathHidden reports whether `path` — or any ancestor folder of it — is in
// the user's hidden set. The ancestor walk means a file deep-linked inside a
// hidden folder is treated as hidden too, not just an exactly-hidden entry.
func localPathHidden(path string, hidden map[string]bool) bool {
	if len(hidden) == 0 {
		return false
	}
	p := strings.Trim(path, "/")
	for p != "" {
		if hidden[p] {
			return true
		}
		i := strings.LastIndex(p, "/")
		if i < 0 {
			break
		}
		p = p[:i]
	}
	return false
}

// LocalHiddenGate refuses to resolve a local (mount,path) the user has hidden
// while the reveal curtain (easter egg) is closed — closing the deep-link bypass
// where ?play=local-… would reveal hidden local media regardless of the curtain.
// It mirrors dropHiddenLocalEntries (the same set that hides the entry from
// listings) and also blocks files inside a hidden folder. Applied to /local/play,
// the player's direct-vs-HLS resolution step (called via axios, so the curtain
// header/?revealHidden is present): blocked → the player never gets a playable
// URL. Curtain open ⇒ hiddenLocalSet is empty ⇒ this is a no-op. Returns 404 (not
// 403) so a hidden file is indistinguishable from a missing one.
func LocalHiddenGate(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.Next() // nothing to gate; let the handler return its own 400
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if localPathHidden(path, hiddenLocalSet(c, s, userID, mount)) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": ErrFileNotFound})
			return
		}
		c.Next()
	}
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
