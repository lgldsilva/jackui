package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
)

const (
	refreshTTLNormal   = 24 * time.Hour      // session-ish — sliding 1 day window
	refreshTTLRemember = 30 * 24 * time.Hour // "lembrar de mim" — sliding 30 days window (eternal as long as user opens app)
)

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Remember bool   `json:"remember"`
}

type tokenResp struct {
	Access    string     `json:"access"`
	Refresh   string     `json:"refresh"`
	ExpiresAt time.Time  `json:"expiresAt"`
	User      *auth.User `json:"user"`
}

// Login handles POST /api/auth/login
func Login(store *auth.Store, tm *auth.TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req loginReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "username + password required"})
			return
		}
		user, err := store.VerifyPassword(req.Username, req.Password)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		// Only active accounts may log in. pending = awaiting admin approval /
		// email confirmation; disabled = blocked by an admin.
		switch user.Status {
		case auth.StatusPending:
			c.JSON(http.StatusForbidden, gin.H{"error": "conta aguardando aprovação ou confirmação de e-mail", "status": "pending"})
			return
		case auth.StatusDisabled:
			c.JSON(http.StatusForbidden, gin.H{"error": "conta desabilitada", "status": "disabled"})
			return
		}
		access, exp, err := tm.SignAccess(user)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "token signing failed"})
			return
		}
		ttl := refreshTTLNormal
		if req.Remember {
			ttl = refreshTTLRemember
		}
		refresh, err := store.CreateRefreshToken(user.ID, ttl, req.Remember)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, tokenResp{Access: access, Refresh: refresh, ExpiresAt: exp, User: user})
	}
}

// Refresh handles POST /api/auth/refresh — body: {refresh}
// Rolling rotation: the old refresh is consumed and a fresh pair is issued.
func Refresh(store *auth.Store, tm *auth.TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct{ Refresh string `json:"refresh"` }
		if err := c.ShouldBindJSON(&req); err != nil || req.Refresh == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "refresh token required"})
			return
		}
		user, remember, err := store.ValidateRefreshToken(req.Refresh)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		// Rotate: invalidate the old refresh token first to prevent re-use
		_ = store.ConsumeRefreshToken(req.Refresh)

		access, exp, err := tm.SignAccess(user)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "token signing failed"})
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

// Logout handles POST /api/auth/logout — body: {refresh}
func Logout(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct{ Refresh string `json:"refresh"` }
		_ = c.ShouldBindJSON(&req)
		if req.Refresh != "" {
			_ = store.ConsumeRefreshToken(req.Refresh)
		}
		c.JSON(http.StatusOK, gin.H{"message": "logged out"})
	}
}

// Me handles GET /api/auth/me — returns the current user from JWT.
func Me(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
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
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
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

// ─── Admin-only user management ─────────────────────────────────────────────

// SetUserStatus handles PATCH /api/auth/users/:id/status (admin only) — approve
// (active), disable, or re-enable an account.
func SetUserStatus(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
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

// DeleteUser handles DELETE /api/auth/users/:id (admin only).
func DeleteUser(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
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
