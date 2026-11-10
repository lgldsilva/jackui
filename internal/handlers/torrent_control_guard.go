package handlers

import (
	"net/http"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/library"
)

// TorrentControlAuthz holds the ownership lookups TorrentControlGuard consults.
// Interfaces so tests need no live streamer; Library may be nil.
type TorrentControlAuthz struct {
	Downloads interface {
		HashOwnerIDs(infoHash string) (map[int]bool, error)
	}
	Library interface {
		GetByHash(userID int, infoHash string) (*library.Entry, error)
	}
	Viewers interface {
		ViewedBy(hash metainfo.Hash, userKey string) bool
	}
}

const errTorrentControlDenied = "not allowed to control this torrent"

// TorrentControlGuard scopes the per-hash swarm controls (pause, resume,
// torrent/file priority) to whoever the torrent belongs to:
//   - admins (a device key never counts as admin, same as AdminOnly);
//   - a user holding a download row for the hash;
//   - for a stream-only torrent (no download row at all): a user watching it
//     (fresh viewer lease) or with it in their library.
//
// Before this, any non-guest could pause another user's queued download; the
// worker didn't notice and the stall detector eventually demoted/failed it.
func TorrentControlGuard(a TorrentControlAuthz) gin.HandlerFunc {
	return func(c *gin.Context) {
		var h metainfo.Hash
		if err := h.FromHexString(c.Param("hash")); err != nil {
			c.Next() // let the handler answer its own 400
			return
		}
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusForbidden, errTorrentControlDenied)
			c.Abort()
			return
		}
		if claims.Role == auth.RoleAdmin && claims.AMR != auth.AMRDevice {
			c.Next()
			return
		}
		allowed, err := a.userMayControl(c, h, claims.UserID)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			c.Abort()
			return
		}
		if !allowed {
			httpshared.RespondErrorMessage(c, http.StatusForbidden, errTorrentControlDenied)
			c.Abort()
			return
		}
		c.Next()
	}
}

func (a TorrentControlAuthz) userMayControl(c *gin.Context, h metainfo.Hash, userID int) (bool, error) {
	hex := h.HexString()
	owners, err := a.Downloads.HashOwnerIDs(hex)
	if err != nil {
		return false, err
	}
	if len(owners) > 0 {
		return owners[userID], nil
	}
	if a.Viewers != nil && a.Viewers.ViewedBy(h, auth.UserKey(c)) {
		return true, nil
	}
	if a.Library == nil {
		return false, nil
	}
	e, err := a.Library.GetByHash(userID, hex)
	return e != nil, err
}
