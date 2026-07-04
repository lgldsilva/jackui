package handlers

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
)

// DownloadDestination is a writable target a user may pick for a download (#16):
// a configured external mount they're allowed to see (UserSubpath mounts already
// resolved to mount/<username>) or a promote destination.
type DownloadDestination struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	UserSubpath bool   `json:"userSubpath,omitempty"`
}

// DestinationService resolves and validates the destinations a user may pick for
// a download. It composes the configured external mounts (filtered by
// AllowedUsers; UserSubpath resolved per-user) with the promote destinations
// (sharedDir + promote_dirs). A nil service disables destination selection
// (handlers then ignore any destBase the client sent).
type DestinationService struct {
	Mounts      []config.ExternalMount
	Promote     []httpshared.PromoteDest
	SharedDir   string
	ResolveUser func(userID int) string
}

func (ds *DestinationService) username(userID int) string {
	if ds == nil || ds.ResolveUser == nil {
		return ""
	}
	return ds.ResolveUser(userID)
}

// For returns the destinations visible to the user, in display order (mounts
// first, then promote destinations).
func (ds *DestinationService) For(userID int) []DownloadDestination {
	out := []DownloadDestination{}
	if ds == nil {
		return out
	}
	user := ds.username(userID)
	for _, m := range ds.Mounts {
		if !mountVisibleTo(m, user) {
			continue
		}
		p := m.Path
		if m.UserSubpath && user != "" {
			p = filepath.Join(p, user)
		}
		out = append(out, DownloadDestination{Name: m.Name, Path: p, UserSubpath: m.UserSubpath})
	}
	for _, d := range BuildPromoteDests(ds.SharedDir, ds.Promote) {
		out = append(out, DownloadDestination{Name: d.Name, Path: d.Path})
	}
	return out
}

// Resolve validates a chosen (base, subdir) against the user's destinations and
// returns the canonical base + cleaned subdir to persist. An empty base is valid
// and means "use the default download dir" (returns "","",nil). A base that
// isn't one of the user's destinations is rejected — defense against a client
// pointing a download at an arbitrary path.
func (ds *DestinationService) Resolve(userID int, base, subdir string) (string, string, error) {
	if base == "" {
		return "", "", nil
	}
	for _, d := range ds.For(userID) {
		if d.Path == base {
			sub, err := httpshared.SanitizeSubdir(subdir)
			if err != nil {
				return "", "", err
			}
			return d.Path, sub, nil
		}
	}
	return "", "", errors.New("destino inválido: " + base)
}

func mountVisibleTo(m config.ExternalMount, user string) bool {
	if len(m.AllowedUsers) == 0 {
		return true
	}
	for _, u := range m.AllowedUsers {
		if u == user {
			return true
		}
	}
	return false
}

// DownloadsDestinations handles GET /api/downloads/destinations — the writable
// targets the current user may pick for a download.
func DownloadsDestinations(ds *DestinationService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		c.JSON(http.StatusOK, ds.For(userID))
	}
}

// DownloadsDestinationBrowse handles GET /api/downloads/dest/browse?base=&path=
// — lists subfolders under {base}/{path} so the picker can navigate. base must be
// one of the user's destinations; path is validated against traversal.
func DownloadsDestinationBrowse(ds *DestinationService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		base, _, err := ds.Resolve(userID, c.Query("base"), "")
		if err != nil || base == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "destino inválido"})
			return
		}
		sub, err := httpshared.SanitizeSubdir(c.Query("path"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		entries, err := os.ReadDir(joinIfSub(base, sub))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"dirs": []string{}, "path": sub})
			return
		}
		c.JSON(http.StatusOK, gin.H{"dirs": httpshared.ListDirs(entries), "path": sub})
	}
}
