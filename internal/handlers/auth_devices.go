package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
)

// Paired-device management for Settings → Sessions → "Dispositivos".

// ListDevices handles GET /api/auth/devices — the caller's paired devices;
// an admin may pass ?all=1 to see every user's.
func ListDevices(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, isAdmin, ok := auth.UserIDFromCtx(c)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		owner := uid
		if isAdmin && c.Query("all") == "1" {
			owner = -1
		}
		keys, err := store.ListDeviceKeys(owner)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"devices": keys})
	}
}

// deviceKeyParam parses :id and the caller; writes the error response on failure.
func deviceKeyParam(c *gin.Context) (id int64, uid int, isAdmin, ok bool) {
	uid, isAdmin, authed := auth.UserIDFromCtx(c)
	if !authed {
		httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
		return 0, 0, false, false
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpshared.RespondErrorMessage(c, http.StatusBadRequest, "invalid id")
		return 0, 0, false, false
	}
	return id, uid, isAdmin, true
}

func respondDeviceKeyErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrDeviceKeyNotFound):
		httpshared.RespondError(c, http.StatusNotFound, err)
	case errors.Is(err, auth.ErrDevicePINFormat):
		httpshared.RespondError(c, http.StatusBadRequest, err)
	default:
		httpshared.RespondError(c, http.StatusInternalServerError, err)
	}
}

// RevokeDevice handles DELETE /api/auth/devices/:id — own key, or any as admin.
// Takes effect on the device's next /device/session (access JWT ≤ its TTL).
func RevokeDevice(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, uid, isAdmin, ok := deviceKeyParam(c)
		if !ok {
			return
		}
		owner := uid
		if isAdmin {
			owner = -1
		}
		if err := store.RevokeDeviceKey(id, owner); err != nil {
			respondDeviceKeyErr(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// SetDevicePIN handles POST /api/auth/devices/:id/pin — body {pin}; "" clears.
// Owner only (an admin can revoke but not set someone else's profile PIN).
func SetDevicePIN(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, uid, _, ok := deviceKeyParam(c)
		if !ok {
			return
		}
		var req struct {
			PIN string `json:"pin"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "invalid body")
			return
		}
		if err := store.SetDevicePIN(id, uid, req.PIN); err != nil {
			respondDeviceKeyErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"hasPin": req.PIN != ""})
	}
}
