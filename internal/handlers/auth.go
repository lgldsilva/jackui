package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "username + password required"})
			return
		}
		if respondIfLocked(c, lockout, req.Username) {
			return
		}
		user, err := store.VerifyPassword(req.Username, req.Password)
		if err != nil {
			lockout.Fail(req.Username)
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		if respondIfInactive(c, user.Status) {
			return
		}
		if !verifyMFA(c, store, lockout, user, req.Totp) {
			return
		}
		lockout.Reset(req.Username)
		resp, err := issueTokens(store, tm, user, req.Remember, c.Request.UserAgent(), c.ClientIP())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": errTokenSigningFailed})
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}

func respondIfLocked(c *gin.Context, lockout *auth.Lockout, username string) bool {
	locked, rem := lockout.Locked(username)
	if !locked {
		return false
	}
	c.Header("Retry-After", strconv.Itoa(int(rem.Seconds())+1))
	c.JSON(http.StatusTooManyRequests, gin.H{
		"error": "muitas tentativas — tente novamente em " + rem.Round(time.Second).String(),
	})
	return true
}

func respondIfInactive(c *gin.Context, status auth.Status) bool {
	switch status {
	case auth.StatusPending:
		c.JSON(http.StatusForbidden, gin.H{"error": "conta aguardando aprovação ou confirmação de e-mail", "status": "pending"})
		return true
	case auth.StatusDisabled:
		c.JSON(http.StatusForbidden, gin.H{"error": "conta desabilitada", "status": "disabled"})
		return true
	}
	return false
}

func verifyMFA(c *gin.Context, store *auth.Store, lockout *auth.Lockout, user *auth.User, totp string) bool {
	if !user.MfaEnabled {
		return true
	}
	if totp == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "código MFA obrigatório", "mfaRequired": true})
		return false
	}
	secret, _, _ := store.GetTOTPSecret(user.ID)
	if auth.ValidateTOTP(secret, totp) || store.ConsumeBackupCode(user.ID, totp) {
		return true
	}
	lockout.Fail(user.Username)
	c.JSON(http.StatusUnauthorized, gin.H{"error": "código MFA inválido", "mfaRequired": true})
	return false
}

func issueTokens(store *auth.Store, tm *auth.TokenManager, user *auth.User, remember bool, userAgent, ip string) (tokenResp, error) {
	access, exp, err := tm.SignAccess(user)
	if err != nil {
		return tokenResp{}, err
	}
	ttl := refreshTTLNormal
	if remember {
		ttl = refreshTTLRemember
	}
	refresh, err := store.CreateRefreshToken(user.ID, ttl, remember, userAgent, ip)
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
		if err := c.ShouldBindJSON(&req); err != nil || req.Refresh == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "refresh token required"})
			return
		}
		// Atomic rotation with a grace window: distinguishes a benign concurrent
		// refresh (reissue) from a real replay of a long-rotated token (revoke).
		user, remember, outcome, err := store.RotateRefreshToken(req.Refresh, refreshGrace)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !refreshOutcomeOK(c, store, user, outcome) {
			return
		}
		resp, err := issueTokenPair(store, tm, user, remember, c.Request.UserAgent(), c.ClientIP())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}

// refreshOutcomeOK writes the error response for a failed rotation and returns false.
// true means the caller may issue a new token pair.
func refreshOutcomeOK(c *gin.Context, store *auth.Store, user *auth.User, outcome auth.RefreshOutcome) bool {
	switch outcome {
	case auth.RefreshInvalid:
		c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh token inválido"})
		return false
	case auth.RefreshReuse:
		// Rotated token replayed after the grace → treat as theft: revoke all.
		_ = store.RevokeAllSessions(user.ID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh token inválido"})
		return false
	}
	// Disabled/pending accounts must not keep renewing access.
	if user.Status != auth.StatusActive && user.Status != "" {
		_ = store.RevokeAllSessions(user.ID)
		c.JSON(http.StatusForbidden, gin.H{"error": "conta inativa", "status": string(user.Status)})
		return false
	}
	return true
}

// issueTokenPair signs a fresh access token and sliding-window refresh token.
func issueTokenPair(store *auth.Store, tm *auth.TokenManager, user *auth.User, remember bool, ua, ip string) (tokenResp, error) {
	access, exp, err := tm.SignAccess(user)
	if err != nil {
		return tokenResp{}, fmt.Errorf("%s", errTokenSigningFailed)
	}
	ttl := refreshTTLNormal
	if remember {
		ttl = refreshTTLRemember
	}
	newRefresh, err := store.CreateRefreshToken(user.ID, ttl, remember, ua, ip)
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
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		user, err := store.GetUserByID(claims.UserID)
		if err != nil || user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
			return
		}
		token, exp, err := tm.SignMedia(user)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": errTokenSigningFailed})
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": token, "expiresAt": exp})
	}
}

// Logout handles POST /api/auth/logout — body: {refresh}
// incognitoCleanable is satisfied by history.Store and library.Store.
type incognitoCleanable interface {
	DeleteIncognito(userID int) error
	DeleteAllIncognito() error
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
		_ = cl.DeleteAllIncognito()
	}
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
			_ = cl.DeleteIncognito(uid)
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
		if req.Refresh != "" {
			_ = store.ConsumeRefreshToken(req.Refresh)
		}
		// Clean up any incognito history/library entries for this user.
		if claims, ok := auth.ClaimsFromCtx(c); ok {
			for _, cl := range cleaners {
				_ = cl.DeleteIncognito(claims.UserID)
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
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		for _, cl := range cleaners {
			if err := cl.DeleteIncognito(claims.UserID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		var req struct {
			Refresh string `json:"refresh"`
		}
		_ = c.ShouldBindJSON(&req)
		sessions, err := store.ListSessions(claims.UserID, req.Refresh)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		if err := store.RevokeSession(claims.UserID, c.Param("id")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "sessão encerrada"})
	}
}

// RevokeOtherSessions handles POST /api/auth/sessions/revoke-others — body
// {refresh} (the session to KEEP). Logs out every other device.
func RevokeOtherSessions(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		var req struct {
			Refresh string `json:"refresh"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Refresh == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "refresh token required"})
			return
		}
		n, err := store.RevokeOtherSessions(claims.UserID, req.Refresh)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "outras sessões encerradas", "revoked": n})
	}
}

// Me handles GET /api/auth/me — returns the current user from JWT.
func Me(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		user, err := store.GetUserByID(claims.UserID)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, user)
	}
}

// ChangePassword handles POST /api/auth/password — the logged-in user changes
// their own password (must supply the current one). When the optional refresh
// token rides along, every OTHER session is revoked (a password change usually
// means "someone may know the old one"), keeping this device logged in.
func ChangePassword(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		var req struct {
			Current string `json:"current"`
			New     string `json:"new"`
			Refresh string `json:"refresh"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.New == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "senha atual e nova são obrigatórias"})
			return
		}
		if len(req.New) < 6 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "a nova senha precisa ter ao menos 6 caracteres"})
			return
		}
		if err := store.ChangePassword(claims.UserID, req.Current, req.New); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		revoked := 0
		if req.Refresh != "" {
			revoked, _ = store.RevokeOtherSessions(claims.UserID, req.Refresh)
		}
		c.JSON(http.StatusOK, gin.H{"message": "senha alterada", "revoked": revoked})
	}
}

// ─── MFA (TOTP) — opt-in per user ───────────────────────────────────────────
// MFA functions moved to auth_mfa.go

// ─── Admin-only user management ─────────────────────────────────────────────
// Admin/user/notification functions moved to auth_admin.go
