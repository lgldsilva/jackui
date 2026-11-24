package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbutil"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
)

const (
	errTokenSigningFailed = "token signing failed"
	errNotAuthenticated   = "not authenticated"
)

const (
	refreshTTLNormal   = 24 * time.Hour      // session-ish — sliding 1 day window
	refreshTTLRemember = 30 * 24 * time.Hour // "lembrar de mim" — sliding 30 days window (eternal as long as user opens app)
	// refreshGrace tolerates concurrent refreshes of the same token (multi-tab, or
	// the burst of retried requests when the backend returns from a deploy): a token
	// rotated within this window reissues a fresh pair instead of revoking the whole
	// session family. Real reuse (a replay after the window) still revokes.
	refreshGrace = 30 * time.Second
)

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Remember bool   `json:"remember"`
	Totp     string `json:"totp"` // 2nd factor when the account has MFA enabled
}

type tokenResp struct {
	Access    string     `json:"access"`
	Refresh   string     `json:"refresh"`
	ExpiresAt time.Time  `json:"expiresAt"`
	User      *auth.User `json:"user"`
}

func Login(store *auth.Store, tm *auth.TokenManager, lockout *auth.Lockout) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req loginReq
		if err := c.ShouldBindJSON(&req); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "username + password required")
			return
		}
		if respondIfLocked(c, lockout, req.Username) {
			return
		}
		user, err := store.VerifyPassword(c.Request.Context(), req.Username, req.Password)
		if err != nil {
			lockout.Fail(req.Username)
			httpshared.RespondError(c, http.StatusUnauthorized, err)
			return
		}
		if respondIfInactive(c, user.Status) {
			return
		}
		if !verifyMFA(c, store, lockout, user, req.Totp) {
			return
		}
		lockout.Reset(req.Username)
		resp, err := issueTokens(c.Request.Context(), store, tm, user, req.Remember, c.Request.UserAgent(), c.ClientIP())
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusInternalServerError, errTokenSigningFailed)
			return
		}
		writeTokens(c, resp, req.Remember)
	}
}

func respondIfLocked(c *gin.Context, lockout *auth.Lockout, username string) bool {
	locked, rem := lockout.Locked(username)
	if !locked {
		return false
	}
	c.Header("Retry-After", strconv.Itoa(int(rem.Seconds())+1))
	httpshared.RespondErrorMessage(c, http.StatusTooManyRequests, "too many attempts — retry in "+rem.Round(time.Second).String())
	return true
}

func respondIfInactive(c *gin.Context, status auth.Status) bool {
	switch status {
	case auth.StatusPending:
		httpshared.RespondErrorMessageFields(c, http.StatusForbidden, "account awaiting approval or email confirmation", gin.H{"status": "pending"})
		return true
	case auth.StatusDisabled:
		httpshared.RespondErrorMessageFields(c, http.StatusForbidden, "account disabled", gin.H{"status": "disabled"})
		return true
	}
	return false
}

func verifyMFA(c *gin.Context, store *auth.Store, lockout *auth.Lockout, user *auth.User, totp string) bool {
	if !user.MfaEnabled {
		return true
	}
	if totp == "" {
		httpshared.RespondErrorMessageFields(c, http.StatusUnauthorized, "MFA code required", gin.H{"mfaRequired": true})
		return false
	}
	secret, _, _ := store.GetTOTPSecret(c.Request.Context(), user.ID)
	// ValidateTOTPForUser (not plain ValidateTOTP) records the matched step so
	// the same code can't be replayed within its ±1 window.
	if store.ValidateTOTPForUser(user.ID, secret, totp) || store.ConsumeBackupCode(c.Request.Context(), user.ID, totp) {
		return true
	}
	lockout.Fail(user.Username)
	httpshared.RespondErrorMessageFields(c, http.StatusUnauthorized, "invalid MFA code", gin.H{"mfaRequired": true})
	return false
}

func issueTokens(ctx context.Context, store *auth.Store, tm *auth.TokenManager, user *auth.User, remember bool, userAgent, ip string) (tokenResp, error) {
	access, exp, err := tm.SignAccess(user)
	if err != nil {
		return tokenResp{}, err
	}
	ttl := refreshTTLNormal
	if remember {
		ttl = refreshTTLRemember
	}
	refresh, err := store.CreateRefreshToken(ctx, user.ID, ttl, remember, userAgent, ip)
	if err != nil {
		return tokenResp{}, err
	}
	return tokenResp{Access: access, Refresh: refresh, ExpiresAt: exp, User: user}, nil
}

// Refresh handles POST /api/auth/refresh — body: {refresh}
// Rolling rotation: the old refresh is consumed and a fresh pair is issued.
func Refresh(store *auth.Store, tm *auth.TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Refresh string `json:"refresh"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			req.Refresh = ""
		}
		// The web sends no body refresh anymore — it relies on the httpOnly
		// cookie (issue #201). Native clients keep the body token.
		if req.Refresh == "" {
			req.Refresh = refreshFromCookie(c)
		}
		if req.Refresh == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "refresh token required")
			return
		}
		// Rotation is consume-old then issue-new across two statements, and the
		// reuse/inactive branches revoke sessions. Cancelling halfway would burn
		// the presented token without a replacement (the retry past the grace
		// window then reads as theft and revokes every session) or skip a
		// revocation, so the whole sequence runs detached from the client.
		ctx, cancel := httpshared.DetachedCtx(c.Request.Context())
		defer cancel()
		// Atomic rotation with a grace window: distinguishes a benign concurrent
		// refresh (reissue) from a real replay of a long-rotated token (revoke).
		user, remember, outcome, err := store.RotateRefreshToken(ctx, req.Refresh, refreshGrace)
		if dbutil.IsReadOnlyErr(err) {
			refreshReadOnly(ctx, c, store, tm, req.Refresh)
			return
		}
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		if !refreshOutcomeOK(ctx, c, store, user, outcome) {
			return
		}
		resp, err := issueTokenPair(ctx, store, tm, user, remember, c.Request.UserAgent(), c.ClientIP())
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		writeTokens(c, resp, remember)
	}
}

// refreshReadOnly serves /refresh while the database is a read-only standby
// (degraded edge): validate without consuming, sign a new access token and hand
// back the SAME refresh token. Reuse detection still rejects, but cannot revoke
// until the primary is back.
func refreshReadOnly(ctx context.Context, c *gin.Context, store *auth.Store, tm *auth.TokenManager, refresh string) {
	user, remember, outcome, err := store.PeekRefreshToken(ctx, refresh, refreshGrace)
	if err != nil {
		httpshared.RespondError(c, http.StatusServiceUnavailable, err)
		return
	}
	if !refreshOutcomeOK(ctx, c, store, user, outcome) {
		return
	}
	access, exp, err := tm.SignAccess(user)
	if err != nil {
		httpshared.RespondErrorMessage(c, http.StatusInternalServerError, errTokenSigningFailed)
		return
	}
	writeTokens(c, tokenResp{Access: access, Refresh: refresh, ExpiresAt: exp, User: user}, remember)
}

// refreshOutcomeOK writes the error response for a failed rotation and returns false.
// true means the caller may issue a new token pair.
func refreshOutcomeOK(ctx context.Context, c *gin.Context, store *auth.Store, user *auth.User, outcome auth.RefreshOutcome) bool {
	switch outcome {
	case auth.RefreshInvalid:
		httpshared.RespondErrorMessage(c, http.StatusUnauthorized, "invalid refresh token")
		return false
	case auth.RefreshReuse:
		// Rotated token replayed after the grace → treat as theft: revoke all.
		_ = store.RevokeAllSessions(ctx, user.ID)
		httpshared.RespondErrorMessage(c, http.StatusUnauthorized, "invalid refresh token")
		return false
	}
	// Disabled/pending accounts must not keep renewing access.
	if user.Status != auth.StatusActive && user.Status != "" {
		_ = store.RevokeAllSessions(ctx, user.ID)
		httpshared.RespondErrorMessageFields(c, http.StatusForbidden, "account inactive", gin.H{"status": string(user.Status)})
		return false
	}
	return true
}

// issueTokenPair signs a fresh access token and sliding-window refresh token.
func issueTokenPair(ctx context.Context, store *auth.Store, tm *auth.TokenManager, user *auth.User, remember bool, ua, ip string) (tokenResp, error) {
	access, exp, err := tm.SignAccess(user)
	if err != nil {
		return tokenResp{}, errors.New(errTokenSigningFailed)
	}
	ttl := refreshTTLNormal
	if remember {
		ttl = refreshTTLRemember
	}
	newRefresh, err := store.CreateRefreshToken(ctx, user.ID, ttl, remember, ua, ip)
	if err != nil {
		return tokenResp{}, err
	}
	return tokenResp{Access: access, Refresh: newRefresh, ExpiresAt: exp, User: user}, nil
}

// MediaToken handles POST /api/auth/media-token — emits a long-lived JWT
// (scope="media") for use as ?token= in <video>/<track>/<img> URLs. The
// regular access token's 15min TTL causes the player to reset playback on
// refresh (the new query string makes <video> treat the src as a new media);
// the media token survives the entire playback session so the URL doesn't
// change mid-stream. Caller must be authenticated via Required middleware.
func MediaToken(store *auth.Store, tm *auth.TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		user, err := store.GetUserByID(c.Request.Context(), claims.UserID)
		if err != nil || user == nil {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, "user not found")
			return
		}
		// A paired device must not shed amr=device by re-minting here.
		sign := tm.SignMedia
		if claims.AMR == auth.AMRDevice {
			sign = tm.SignDeviceMedia
		}
		token, exp, err := sign(user)
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusInternalServerError, errTokenSigningFailed)
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": token, "expiresAt": exp})
	}
}

// Logout handles POST /api/auth/logout — body: {refresh}
// incognitoCleanable is satisfied by history.Store and library.Store.
type incognitoCleanable interface {
	DeleteIncognito(ctx context.Context, userID int) error
	DeleteAllIncognito(ctx context.Context) error
}

// incognitoHeartbeats tracks the last heartbeat time per userID.
// Entries expire after incognitoTTL of inactivity; a background goroutine
// cleans up stale incognito data.
var (
	incognitoMu         sync.Mutex
	incognitoHeartbeats = make(map[int]time.Time)
	incognitoTTL        = time.Hour
)

// StartIncognitoReaper launches a background goroutine that checks for
// incognito sessions that have gone quiet (tab closed / crash) and deletes
// their data after incognitoTTL. Call once at startup.
func StartIncognitoReaper(cleaners ...incognitoCleanable) {
	for _, cl := range cleaners {
		_ = cl.DeleteAllIncognito(context.Background())
	}
	go reaperLoop(cleaners)
}

// StartIncognitoReaperLoop starts only the TTL loop, without the boot-time purge
// of every user's incognito rows. Used by edge nodes that share the database
// with a primary whose incognito sessions may still be live.
func StartIncognitoReaperLoop(cleaners ...incognitoCleanable) {
	go reaperLoop(cleaners)
}

func reaperLoop(cleaners []incognitoCleanable) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		expired := collectExpiredIncognito()
		purgeIncognito(cleaners, expired)
	}
}

func collectExpiredIncognito() []int {
	incognitoMu.Lock()
	defer incognitoMu.Unlock()
	expired := make([]int, 0)
	for uid, last := range incognitoHeartbeats {
		if time.Since(last) > incognitoTTL {
			expired = append(expired, uid)
		}
	}
	for _, uid := range expired {
		delete(incognitoHeartbeats, uid)
	}
	return expired
}

func purgeIncognito(cleaners []incognitoCleanable, uids []int) {
	for _, uid := range uids {
		for _, cl := range cleaners {
			_ = cl.DeleteIncognito(context.Background(), uid)
		}
	}
}

// IncognitoHeartbeat handles POST /api/user/incognito/heartbeat — the frontend
// sends this periodically while incognito mode is active to prevent TTL expiry.
func IncognitoHeartbeat() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.Status(http.StatusUnauthorized)
			return
		}
		incognitoMu.Lock()
		incognitoHeartbeats[claims.UserID] = time.Now()
		incognitoMu.Unlock()
		c.Status(http.StatusNoContent)
	}
}

func Logout(store *auth.Store, cleaners ...incognitoCleanable) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Refresh string `json:"refresh"`
		}
		_ = c.ShouldBindJSON(&req)
		// Web no longer keeps the refresh in localStorage — fall back to the
		// httpOnly cookie (issue #201). Native clients still send the body.
		if req.Refresh == "" {
			req.Refresh = refreshFromCookie(c)
		}
		if req.Refresh != "" {
			// Logout often fires on page unload: the revocation must not be
			// droppable by the connection closing.
			rctx, rcancel := httpshared.DetachedCtx(c.Request.Context())
			defer rcancel()
			_ = store.ConsumeRefreshToken(rctx, req.Refresh)
		}
		// The cookie dies regardless of whether a token was presented.
		clearRefreshCookie(c)
		// Clean up any incognito history/library entries for this user.
		if claims, ok := auth.ClaimsFromCtx(c); ok {
			// Detached: privacy cleanup must finish even if the client drops now.
			ctx, cancel := httpshared.DetachedCtx(c.Request.Context())
			defer cancel()
			for _, cl := range cleaners {
				_ = cl.DeleteIncognito(ctx, claims.UserID)
			}
		}
		c.JSON(http.StatusOK, gin.H{"message": "logged out"})
	}
}

// ClearIncognito handles DELETE /api/user/incognito — deletes all incognito-flagged
// history and library entries for the authenticated user. Called by the frontend
// when the user disables incognito mode.
func ClearIncognito(cleaners ...incognitoCleanable) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, "not authenticated")
			return
		}
		ctx, cancel := httpshared.DetachedCtx(c.Request.Context())
		defer cancel()
		for _, cl := range cleaners {
			if err := cl.DeleteIncognito(ctx, claims.UserID); err != nil {
				httpshared.RespondError(c, http.StatusInternalServerError, err)
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"message": "incognito data cleared"})
	}
}

// ListSessions handles POST /api/auth/sessions — body {refresh} (the caller's
// own token, used only to flag the current session). Returns the user's active
// sessions. POST (not GET) so the refresh token rides in the body, never a URL.
func ListSessions(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		var req struct {
			Refresh string `json:"refresh"`
		}
		_ = c.ShouldBindJSON(&req)
		// The cookie is the web's refresh carrier now (issue #201); the body
		// token stays for native clients.
		if req.Refresh == "" {
			req.Refresh = refreshFromCookie(c)
		}
		sessions, err := store.ListSessions(c.Request.Context(), claims.UserID, req.Refresh)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"sessions": sessions})
	}
}

// RevokeSession handles DELETE /api/auth/sessions/:id — drops one session.
func RevokeSession(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		ctx, cancel := httpshared.DetachedCtx(c.Request.Context())
		defer cancel()
		if err := store.RevokeSession(ctx, claims.UserID, c.Param("id")); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "session terminated"})
	}
}

// RevokeOtherSessions handles POST /api/auth/sessions/revoke-others — body
// {refresh} (the session to KEEP). Logs out every other device.
func RevokeOtherSessions(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		var req struct {
			Refresh string `json:"refresh"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			req.Refresh = ""
		}
		if req.Refresh == "" {
			req.Refresh = refreshFromCookie(c)
		}
		if req.Refresh == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "refresh token required")
			return
		}
		ctx, cancel := httpshared.DetachedCtx(c.Request.Context())
		defer cancel()
		n, err := store.RevokeOtherSessions(ctx, claims.UserID, req.Refresh)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "other sessions terminated", "revoked": n})
	}
}

// Me handles GET /api/auth/me — returns the current user from JWT.
func Me(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		user, err := store.GetUserByID(c.Request.Context(), claims.UserID)
		if err != nil {
			httpshared.RespondError(c, http.StatusUnauthorized, err)
			return
		}
		c.JSON(http.StatusOK, user)
	}
}

// ChangePassword handles POST /api/auth/password — the logged-in user changes
// their own password (must supply the current one). Every OTHER session and
// every paired device is revoked (a password change usually means "someone may
// know the old one"); the session of the optional refresh token is kept so
// this device stays logged in. Without it all sessions are revoked.
func ChangePassword(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		var req struct {
			Current string `json:"current"`
			New     string `json:"new"`
			Refresh string `json:"refresh"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.New == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "current and new password are required")
			return
		}
		if req.Refresh == "" {
			req.Refresh = refreshFromCookie(c)
		}
		if len(req.New) < 6 {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "the new password must be at least 6 characters")
			return
		}
		// change + revoke are one security action: detached so a dropped client
		// cannot leave the other sessions alive under the new password.
		ctx, cancel := httpshared.DetachedCtx(c.Request.Context())
		defer cancel()
		if err := store.ChangePassword(ctx, claims.UserID, req.Current, req.New); err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		revoked, err := revokeAccessAfterPasswordChange(ctx, store, claims.UserID, req.Refresh)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "password changed", "revoked": revoked})
	}
}

// ─── MFA (TOTP) — opt-in per user ───────────────────────────────────────────
// MFA functions moved to auth_mfa.go

// ─── Admin-only user management ─────────────────────────────────────────────
// Admin/user/notification functions moved to auth_admin.go
