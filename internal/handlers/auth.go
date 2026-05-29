package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/config"
)

const (
	errTokenSigningFailed = "token signing failed"
	errNotAuthenticated   = "not authenticated"
)

const (
	refreshTTLNormal   = 24 * time.Hour      // session-ish — sliding 1 day window
	refreshTTLRemember = 30 * 24 * time.Hour // "lembrar de mim" — sliding 30 days window (eternal as long as user opens app)
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
		c.JSON(http.StatusOK, issueTokens(store, tm, user, req.Remember))
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

func issueTokens(store *auth.Store, tm *auth.TokenManager, user *auth.User, remember bool) tokenResp {
	access, exp, _ := tm.SignAccess(user)
	ttl := refreshTTLNormal
	if remember {
		ttl = refreshTTLRemember
	}
	refresh, _ := store.CreateRefreshToken(user.ID, ttl, remember)
	return tokenResp{Access: access, Refresh: refresh, ExpiresAt: exp, User: user}
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
		user, remember, err := store.ValidateRefreshToken(req.Refresh)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		// A disabled/pending account must not be able to keep renewing access — an
		// admin disabling someone takes effect within one access-token TTL.
		if user.Status != auth.StatusActive && user.Status != "" {
			_ = store.RevokeAllSessions(user.ID)
			c.JSON(http.StatusForbidden, gin.H{"error": "conta inativa", "status": string(user.Status)})
			return
		}
		// Rotate: invalidate the old refresh token first to prevent re-use
		_ = store.ConsumeRefreshToken(req.Refresh)

		access, exp, err := tm.SignAccess(user)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": errTokenSigningFailed})
			return
		}
		// Sliding window: new TTL counted FROM NOW. Remember-me stays remember-me eternally.
		ttl := refreshTTLNormal
		if remember {
			ttl = refreshTTLRemember
		}
		newRefresh, err := store.CreateRefreshToken(user.ID, ttl, remember)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, tokenResp{Access: access, Refresh: newRefresh, ExpiresAt: exp, User: user})
	}
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
func Logout(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Refresh string `json:"refresh"`
		}
		_ = c.ShouldBindJSON(&req)
		if req.Refresh != "" {
			_ = store.ConsumeRefreshToken(req.Refresh)
		}
		c.JSON(http.StatusOK, gin.H{"message": "logged out"})
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
// their own password (must supply the current one).
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
		c.JSON(http.StatusOK, gin.H{"message": "senha alterada"})
	}
}

// ─── MFA (TOTP) — opt-in per user ───────────────────────────────────────────

// MFAEnrollStart handles POST /api/auth/mfa/enroll — generates a fresh TOTP
// secret (stored as not-yet-enabled) and returns the otpauth URI + secret for
// the user to add to their authenticator app. Confirmed via MFAEnrollVerify.
func MFAEnrollStart(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		secret, err := auth.GenerateTOTPSecret()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := store.SetTOTPSecret(claims.UserID, secret); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		uri := auth.TOTPURI(secret, "JackUI", claims.Username)
		c.JSON(http.StatusOK, gin.H{"secret": secret, "uri": uri})
	}
}

// MFAEnrollVerify handles POST /api/auth/mfa/verify — confirms a code against
// the pending secret and enables MFA.
func MFAEnrollVerify(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		var req struct {
			Code string `json:"code"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "código obrigatório"})
			return
		}
		secret, _, _ := store.GetTOTPSecret(claims.UserID)
		if !auth.ValidateTOTP(secret, req.Code) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "código inválido"})
			return
		}
		if err := store.EnableTOTP(claims.UserID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Issue recovery codes immediately so the user can't lock themselves out by
		// losing the authenticator. Shown exactly once.
		codes, err := store.GenerateBackupCodes(claims.UserID, backupCodeCount)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "MFA ativado", "backupCodes": codes})
	}
}

const backupCodeCount = 10

// MFABackupCodesStatus handles GET /api/auth/mfa/backup-codes — how many unused
// recovery codes remain.
func MFABackupCodesStatus(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		c.JSON(http.StatusOK, gin.H{"remaining": store.CountBackupCodes(claims.UserID)})
	}
}

// MFABackupCodesRegenerate handles POST /api/auth/mfa/backup-codes/regenerate —
// replaces all codes with a fresh set (invalidating the old ones). Requires the
// current password so a hijacked session can't silently rotate recovery codes.
func MFABackupCodesRegenerate(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		var req struct {
			Password string `json:"password"`
		}
		_ = c.ShouldBindJSON(&req)
		if _, err := store.VerifyPassword(claims.Username, req.Password); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "senha incorreta"})
			return
		}
		codes, err := store.GenerateBackupCodes(claims.UserID, backupCodeCount)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"backupCodes": codes})
	}
}

// MFADisable handles POST /api/auth/mfa/disable — turns MFA off (requires the
// current password to prevent a hijacked session from removing it).
func MFADisable(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		var req struct {
			Password string `json:"password"`
		}
		_ = c.ShouldBindJSON(&req)
		// Re-verify the password by username (we have it in claims).
		if _, err := store.VerifyPassword(claims.Username, req.Password); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "senha incorreta"})
			return
		}
		if err := store.DisableTOTP(claims.UserID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "MFA desativado"})
	}
}

// ─── Admin-only user management ─────────────────────────────────────────────

// SetUserStatus handles PATCH /api/auth/users/:id/status (admin only) — approve
// (active), disable, or re-enable an account.
func SetUserStatus(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		var req struct {
			Status auth.Status `json:"status"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "status required"})
			return
		}
		if req.Status != auth.StatusActive && req.Status != auth.StatusPending && req.Status != auth.StatusDisabled {
			c.JSON(http.StatusBadRequest, gin.H{"error": "status inválido"})
			return
		}
		claims, _ := auth.ClaimsFromCtx(c)
		if claims != nil && claims.UserID == id && req.Status != auth.StatusActive {
			c.JSON(http.StatusBadRequest, gin.H{"error": "não pode desabilitar a si mesmo"})
			return
		}
		if err := store.SetStatus(id, req.Status); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Disabling kills active sessions so the block takes effect within one
		// access-token TTL instead of waiting for the refresh token to expire.
		if req.Status == auth.StatusDisabled {
			_ = store.RevokeAllSessions(id)
		}
		c.JSON(http.StatusOK, gin.H{"message": "status atualizado"})
	}
}

type createUserReq struct {
	Username string    `json:"username"`
	Password string    `json:"password"`
	Role     auth.Role `json:"role"`
}

// CreateUser handles POST /api/auth/users (admin only).
func CreateUser(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createUserReq
		if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.Password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "username + password required"})
			return
		}
		id, err := store.CreateUser(req.Username, req.Password, req.Role)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": id})
	}
}

// ListUsers handles GET /api/auth/users (admin only).
func ListUsers(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		users, err := store.ListUsers()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if users == nil {
			users = []auth.User{}
		}
		c.JSON(http.StatusOK, users)
	}
}

// SetNtfyTopic handles POST /api/user/ntfy-topic — updates the logged-in
// user's ntfy.sh notification topic. Body: { topic: string }.
func SetNtfyTopic(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Topic string `json:"topic"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "topic required"})
			return
		}
		claims, _ := auth.ClaimsFromCtx(c)
		if err := store.SetNtfyTopic(claims.UserID, req.Topic); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "tópico atualizado"})
	}
}

// NotifyTest handles POST /api/user/notify-test — sends a test push
// notification to the user's ntfy topic (or the global default). Useful for
// verifying that ntfy is configured correctly from the Settings UI.
func NotifyTest(cfg *config.Config, store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		baseURL := ntfyBaseURL(cfg)
		topic := resolveNtfyTopic(cfg, store, c)
		if topic == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "nenhum tópico ntfy configurado"})
			return
		}
		if !postNtfyNotification(c, baseURL, topic) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "notificação de teste enviada"})
	}
}

func ntfyBaseURL(cfg *config.Config) string {
	if cfg.Notifications.NtfyBaseURL != "" {
		return cfg.Notifications.NtfyBaseURL
	}
	return "https://ntfy.sh"
}

func resolveNtfyTopic(cfg *config.Config, store *auth.Store, c *gin.Context) string {
	topic := cfg.Notifications.NtfyDefaultTopic
	if store == nil {
		return topic
	}
	claims, ok := auth.ClaimsFromCtx(c)
	if !ok {
		return topic
	}
	user, err := store.GetUserByID(claims.UserID)
	if err != nil || user.NtfyTopic == "" {
		return topic
	}
	return user.NtfyTopic
}

func postNtfyNotification(c *gin.Context, baseURL, topic string) bool {
	url := fmt.Sprintf("%s/%s", strings.TrimRight(baseURL, "/"), topic)
	host, _ := os.Hostname()
	body := fmt.Sprintf("Notificação de teste do JackUI (%s)", host)
	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", url, bytes.NewBufferString(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	req.Header.Set("Title", "JackUI — Teste de Notificação")
	req.Header.Set("Tags", "test,rocket")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("ntfy retornou %d", resp.StatusCode)})
		return false
	}
	return true
}

// DeleteUser handles DELETE /api/auth/users/:id (admin only).
func DeleteUser(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		claims, _ := auth.ClaimsFromCtx(c)
		if claims != nil && claims.UserID == id {
			c.JSON(http.StatusBadRequest, gin.H{"error": "não pode deletar a si mesmo"})
			return
		}
		if err := store.DeleteUser(id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "deleted"})
	}
}
