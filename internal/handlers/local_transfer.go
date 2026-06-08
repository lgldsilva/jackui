package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/localstream"
)

// LocalTransferStatus handles GET /api/local/transfer-status?mount=&path= and
// reports how fast bytes are being pulled from the mount for a file that is
// currently playing (direct or HLS). The player polls this so the UI can show
// "downloading X MB/s" / "waiting for data" — essential on rclone/Drive mounts
// where a play silently fetches over the network.
//
// Returns 200 with active:false (NOT 404) when no session exists yet: the
// player polls before/while the stream spins up, and a 404 would read as an
// error rather than "not started".
func LocalTransferStatus(b *local.Browser, reg *localstream.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPathParam})
			return
		}
		// Scope-check first so a user can't probe another user's transfer.
		if !checkMountAccess(b, c, mount) {
			return
		}
		if reg == nil {
			c.JSON(http.StatusOK, localstream.Snapshot{Active: false})
			return
		}
		scoped := scopePath(b, c, mount, path)
		// HLS first (the transcode path), then direct — a file uses one or the
		// other, but checking both keeps the endpoint agnostic to the decision.
		if sess, ok := reg.Get(transferKeyHLS(mount, scoped)); ok {
			c.JSON(http.StatusOK, sess.Snapshot())
			return
		}
		if sess, ok := reg.Get(transferKeyDirect(mount, scoped)); ok {
			c.JSON(http.StatusOK, sess.Snapshot())
			return
		}
		c.JSON(http.StatusOK, localstream.Snapshot{Active: false})
	}
}
