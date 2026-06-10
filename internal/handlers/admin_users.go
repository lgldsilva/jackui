package handlers

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/mailer"
)

const errUserNotFound = "usuário não encontrado"

// userFromIDParam resolves the :id route param to an existing user, answering
// 400/404 itself. Returns nil when the response was already written.
func userFromIDParam(c *gin.Context, store *auth.Store) *auth.User {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
		return nil
	}
	user, err := store.GetUserByID(id)
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": errUserNotFound})
		return nil
	}
	return user
}

// AdminResetPassword handles POST /api/auth/users/:id/reset-password (admin).
// With a password (≥6) it overwrites it directly; without one it issues a
// single-use reset link (1h TTL, Invite-style: always returned, emailed when
// the account has an address). Either way every session of the target user is
// revoked — the whole point of a reset is locking out whoever held the account.
func AdminResetPassword(store *auth.Store, mlr *mailer.Mailer, cfgBaseURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := userFromIDParam(c, store)
		if user == nil {
			return
		}
		var req struct {
			Password string `json:"password"`
		}
		_ = c.ShouldBindJSON(&req)
		if req.Password != "" {
			if len(req.Password) < 6 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "a nova senha precisa ter ao menos 6 caracteres"})
				return
			}
			if err := store.SetPassword(user.ID, req.Password); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			_ = store.RevokeAllSessions(user.ID)
			c.JSON(http.StatusOK, gin.H{"message": "senha redefinida"})
			return
		}
		tok, err := store.CreateToken(auth.TokenResetPassword, user.ID, user.Email, resetTTL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		link := baseURL(c, cfgBaseURL) + "/reset-password?token=" + tok
		if user.Email != "" {
			notify(mlr, user.Email, "JackUI — recuperar senha", "Um administrador iniciou a redefinição da sua senha:", link)
		}
		_ = store.RevokeAllSessions(user.ID)
		c.JSON(http.StatusOK, gin.H{"link": link})
	}
}

// AdminListUserSessions handles GET /api/auth/users/:id/sessions (admin) —
// the target's active sessions (none flagged "current": the admin isn't them).
func AdminListUserSessions(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := userFromIDParam(c, store)
		if user == nil {
			return
		}
		sessions, err := store.ListSessions(user.ID, "")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"sessions": sessions})
	}
}

// AdminRevokeUserSession handles DELETE /api/auth/users/:id/sessions/:sid (admin).
func AdminRevokeUserSession(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := userFromIDParam(c, store)
		if user == nil {
			return
		}
		if err := store.RevokeSession(user.ID, c.Param("sid")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "sessão encerrada"})
	}
}

// AdminRevokeUserSessions handles DELETE /api/auth/users/:id/sessions (admin) —
// logs the target user out of every device.
func AdminRevokeUserSessions(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := userFromIDParam(c, store)
		if user == nil {
			return
		}
		if err := store.RevokeAllSessions(user.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "sessões encerradas"})
	}
}

// emailFormat is intentionally loose (something@something.tld) — real
// validation happens by clicking the verification link.
var emailFormat = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// ChangeEmail handles POST /api/auth/email — the logged-in user changes their
// own address. Requires the current password (a hijacked access token must not
// be able to redirect recovery emails) and re-triggers verification.
func ChangeEmail(store *auth.Store, mlr *mailer.Mailer, cfgBaseURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		var req struct {
			Password string `json:"password"`
			Email    string `json:"email"`
		}
		_ = c.ShouldBindJSON(&req)
		email := strings.TrimSpace(strings.ToLower(req.Email))
		if !emailFormat.MatchString(email) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "e-mail inválido"})
			return
		}
		if _, err := store.VerifyPassword(claims.Username, req.Password); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "senha incorreta"})
			return
		}
		used, err := store.EmailInUse(email, claims.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if used {
			c.JSON(http.StatusConflict, gin.H{"error": "e-mail já cadastrado"})
			return
		}
		if err := store.UpdateEmail(claims.UserID, email); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		sendVerifyEmail(store, mlr, c, cfgBaseURL, claims.UserID, email)
		c.JSON(http.StatusOK, gin.H{"message": "e-mail atualizado — confirme pelo link enviado"})
	}
}
