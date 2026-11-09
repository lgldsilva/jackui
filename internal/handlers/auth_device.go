package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
)

// Device pairing (issue #201): a TV shows a QR → the user approves on a phone
// that is already logged in → the TV receives a long-lived, revocable device
// key and trades it for short-lived access JWTs (amr=device).

const deviceFieldMax = 64

type deviceStartReq struct {
	DeviceID string `json:"deviceId"`
	Name     string `json:"name"`
	Model    string `json:"model"`
}

// cleanDeviceField trims and caps free-text device metadata (it is rendered on
// the approve page and in the Sessions list).
func cleanDeviceField(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
	if len(s) > deviceFieldMax {
		s = s[:deviceFieldMax]
	}
	return s
}

// DeviceStart handles POST /api/auth/device/start (public) — begins a pairing.
func DeviceStart(flow *auth.DeviceFlow) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req deviceStartReq
		if err := c.ShouldBindJSON(&req); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "invalid body")
			return
		}
		meta := auth.DeviceKeyMeta{
			DeviceID: cleanDeviceField(req.DeviceID),
			Name:     cleanDeviceField(req.Name),
			Model:    cleanDeviceField(req.Model),
			IP:       c.ClientIP(),
		}
		if meta.DeviceID == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "deviceId required")
			return
		}
		if meta.Name == "" {
			meta.Name = "TV"
		}
		deviceCode, p, err := flow.Start(meta)
		if errors.Is(err, auth.ErrDeviceFlowFull) {
			httpshared.RespondError(c, http.StatusTooManyRequests, err)
			return
		}
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		userCode := auth.FormatUserCode(p.UserCode)
		c.JSON(http.StatusOK, gin.H{
			"deviceCode":       deviceCode,
			"userCode":         userCode,
			"verificationPath": "/link?code=" + userCode,
			"expiresIn":        int(auth.DeviceFlowTTL.Seconds()),
			"interval":         int(flow.Interval().Seconds()),
		})
	}
}

// deviceSessionResp is what a paired device receives on token/session.
type deviceSessionResp struct {
	DeviceKey      string     `json:"deviceKey,omitempty"`
	DeviceKeyID    int64      `json:"deviceKeyId"`
	DeviceID       string     `json:"deviceId"`
	Access         string     `json:"access"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	MediaToken     string     `json:"mediaToken"`
	MediaExpiresAt time.Time  `json:"mediaExpiresAt"`
	User           *auth.User `json:"user"`
}

func signDeviceSession(tm *auth.TokenManager, user *auth.User) (deviceSessionResp, error) {
	access, exp, err := tm.SignDeviceAccess(user)
	if err != nil {
		return deviceSessionResp{}, err
	}
	media, mexp, err := tm.SignMedia(user)
	if err != nil {
		return deviceSessionResp{}, err
	}
	return deviceSessionResp{Access: access, ExpiresAt: exp, MediaToken: media, MediaExpiresAt: mexp, User: user}, nil
}

// DeviceToken handles POST /api/auth/device/token (public) — the TV polls it
// with its device code; RFC 8628 error codes until approved, then ONCE the
// device key + first access token.
func DeviceToken(flow *auth.DeviceFlow, store *auth.Store, tm *auth.TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			DeviceCode string `json:"deviceCode"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.DeviceCode == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "deviceCode required")
			return
		}
		poll := flow.Poll(req.DeviceCode)
		if poll.Err != "" {
			fields := gin.H{}
			if poll.Err == auth.DeviceErrSlowDown {
				fields["interval"] = int(flow.Interval().Seconds())
			}
			httpshared.RespondErrorMessageFields(c, http.StatusBadRequest, poll.Err, fields)
			return
		}
		user, err := store.GetUserByID(poll.UserID)
		if err != nil || user.Status != auth.StatusActive {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, auth.DeviceErrDenied)
			return
		}
		p := poll.Pairing
		key, id, err := store.CreateDeviceKey(user.ID, auth.DeviceKeyMeta{DeviceID: p.DeviceID, Name: p.Name, Model: p.Model, IP: c.ClientIP()})
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		resp, err := signDeviceSession(tm, user)
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusInternalServerError, errTokenSigningFailed)
			return
		}
		resp.DeviceKey, resp.DeviceKeyID, resp.DeviceID = key, id, p.DeviceID
		c.JSON(http.StatusOK, resp)
	}
}

// deviceKeyAuth resolves the "Authorization: Device …" key; writes 401 on failure.
func deviceKeyAuth(c *gin.Context, store *auth.Store) (*auth.DeviceKey, bool) {
	k, err := store.LookupDeviceKey(auth.DeviceKeyFromHeader(c))
	if err != nil {
		httpshared.RespondErrorMessage(c, http.StatusUnauthorized, "invalid device key")
		return nil, false
	}
	return k, true
}

// DeviceSession handles POST /api/auth/device/session (Device key) — trades the
// key (+ profile PIN when set) for a fresh access JWT + media token.
func DeviceSession(store *auth.Store, tm *auth.TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		k, ok := deviceKeyAuth(c, store)
		if !ok {
			return
		}
		var req struct {
			PIN string `json:"pin"`
		}
		_ = c.ShouldBindJSON(&req) // body is optional (no PIN set)
		if !checkDevicePIN(c, store, k, req.PIN) {
			return
		}
		user, err := store.GetUserByID(k.UserID)
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, "invalid device key")
			return
		}
		if respondIfInactive(c, user.Status) {
			return
		}
		resp, err := signDeviceSession(tm, user)
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusInternalServerError, errTokenSigningFailed)
			return
		}
		_ = store.TouchDeviceKey(k.ID, c.ClientIP())
		resp.DeviceKeyID, resp.DeviceID = k.ID, k.DeviceID
		c.JSON(http.StatusOK, resp)
	}
}

func checkDevicePIN(c *gin.Context, store *auth.Store, k *auth.DeviceKey, pin string) bool {
	err := store.CheckDevicePIN(k, pin, time.Now())
	switch {
	case err == nil:
		return true
	case errors.Is(err, auth.ErrDevicePINRequired), errors.Is(err, auth.ErrDevicePINInvalid):
		httpshared.RespondErrorFields(c, http.StatusUnauthorized, err, gin.H{"pinRequired": true})
	case errors.Is(err, auth.ErrDevicePINLocked):
		httpshared.RespondErrorFields(c, http.StatusTooManyRequests, err, gin.H{"pinRequired": true})
	default:
		httpshared.RespondError(c, http.StatusInternalServerError, err)
	}
	return false
}

// DeviceProfiles handles GET /api/auth/device/profiles (Device key) — the users
// paired on the caller's device, for the TV profile picker.
func DeviceProfiles(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		k, ok := deviceKeyAuth(c, store)
		if !ok {
			return
		}
		keys, err := store.DeviceProfiles(k.DeviceID)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		out := make([]gin.H, 0, len(keys))
		for _, p := range keys {
			out = append(out, gin.H{"deviceKeyId": p.ID, "userId": p.UserID, "username": p.Username, "hasPin": p.HasPIN})
		}
		c.JSON(http.StatusOK, gin.H{"deviceId": k.DeviceID, "profiles": out})
	}
}

// pairingView is the approve page's view of a pending pairing.
func pairingView(p auth.DevicePairing) gin.H {
	return gin.H{
		"userCode": auth.FormatUserCode(p.UserCode), "name": p.Name, "model": p.Model,
		"ip": p.IP, "createdAt": p.CreatedAt, "expiresAt": p.ExpiresAt,
	}
}

// respondUnknownCode counts a failed user-code lookup against the caller and
// answers 404 (or 429 once locked), so codes can't be enumerated.
func respondUnknownCode(c *gin.Context, lockout *auth.Lockout, key string) {
	lockout.Fail(key)
	httpshared.RespondError(c, http.StatusNotFound, auth.ErrDeviceCodeNotFound)
}

func deviceLookupKey(uid int) string { return "device-link:" + strconv.Itoa(uid) }

// DevicePending handles GET /api/auth/device/pending?code= (JWT) — what the
// approve page shows before the user confirms.
func DevicePending(flow *auth.DeviceFlow, lockout *auth.Lockout) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, _, _ := auth.UserIDFromCtx(c)
		key := deviceLookupKey(uid)
		if respondIfLocked(c, lockout, key) {
			return
		}
		p, err := flow.Lookup(c.Query("code"))
		if err != nil {
			respondUnknownCode(c, lockout, key)
			return
		}
		c.JSON(http.StatusOK, pairingView(p))
	}
}

// DeviceApprove handles POST /api/auth/device/approve (JWT) — body
// {userCode, approve}. Explicit confirmation only; the link alone never pairs.
func DeviceApprove(flow *auth.DeviceFlow, lockout *auth.Lockout) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, _, ok := auth.UserIDFromCtx(c)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		var req struct {
			UserCode string `json:"userCode"`
			Approve  bool   `json:"approve"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.UserCode == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "userCode required")
			return
		}
		key := deviceLookupKey(uid)
		if respondIfLocked(c, lockout, key) {
			return
		}
		p, err := flow.Decide(req.UserCode, uid, req.Approve)
		if err != nil {
			respondUnknownCode(c, lockout, key)
			return
		}
		lockout.Reset(key)
		view := pairingView(p)
		view["approved"] = req.Approve
		c.JSON(http.StatusOK, view)
	}
}
