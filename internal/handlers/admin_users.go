package handlers

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/mailer"
)

const errUserNotFound = "user not found"

// userFromIDParam resolves the :id route param to an existing user, answering
// 400/404 itself. Returns nil when the response was already written.
func userFromIDParam(c *gin.Context, store *auth.Store) *auth.User {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
		return nil
	}
	user, err := store.GetUserByID(c.Request.Context(), id)
	if err != nil || user == nil {
		httpshared.RespondErrorMessage(c, http.StatusNotFound, errUserNotFound)
		return nil
	}
	return user
}

// AdminResetPassword handles POST /api/auth/users/:id/reset-password (admin).
// With a password (≥6) it overwrites it directly; without one it issues a
// single-use reset link (1h TTL, Invite-style: always returned, emailed when
// the account has an address). Either way every session of the target user is
// revoked — the whole point of a reset is locking out whoever held the account.
// Paired devices are revoked once the new password is actually set (here in
// password mode, in Reset when the link is consumed).
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
				httpshared.RespondErrorMessage(c, http.StatusBadRequest, "the new password must be at least 6 characters")
				return
			}
			if err := adminSetPassword(c.Request.Context(), store, user.ID, req.Password); err != nil {
				httpshared.RespondError(c, http.StatusInternalServerError, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"message": "password reset"})
			return
		}
		base := baseURL(c, cfgBaseURL)
		if base == "" {
			httpshared.RespondErrorMessage(c, http.StatusInternalServerError, "public base URL not configured")
			return
		}
		tok, err := store.CreateToken(c.Request.Context(), auth.TokenResetPassword, user.ID, user.Email, resetTTL)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		link := base + "/reset-password?token=" + tok
		if user.Email != "" {
			notify(mlr, user.Email, "JackUI — password recovery", "An administrator started a password reset for your account:", link)
		}
		// Revocation is the security half of the reset: detached so a client
		// that drops after the link was issued cannot leave the sessions alive.
		rctx, rcancel := httpshared.DetachedCtx(c.Request.Context())
		defer rcancel()
		_ = store.RevokeAllSessions(rctx, user.ID)
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
		sessions, err := store.ListSessions(c.Request.Context(), user.ID, "")
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
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
		ctx, cancel := httpshared.DetachedCtx(c.Request.Context())
		defer cancel()
		if err := store.RevokeSession(ctx, user.ID, c.Param("sid")); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "session terminated"})
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
		ctx, cancel := httpshared.DetachedCtx(c.Request.Context())
		defer cancel()
		if err := store.RevokeAllSessions(ctx, user.ID); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "sessions terminated"})
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
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		var req struct {
			Password string `json:"password"`
			Email    string `json:"email"`
		}
		_ = c.ShouldBindJSON(&req)
		email := strings.TrimSpace(strings.ToLower(req.Email))
		if !emailFormat.MatchString(email) {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "invalid email")
			return
		}
		if _, err := store.VerifyPassword(c.Request.Context(), claims.Username, req.Password); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "incorrect password")
			return
		}
		used, err := store.EmailInUse(c.Request.Context(), email, claims.UserID)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		if used {
			httpshared.RespondErrorMessage(c, http.StatusConflict, "email already registered")
			return
		}
		if err := store.UpdateEmail(c.Request.Context(), claims.UserID, email); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		sendVerifyEmail(c.Request.Context(), store, mlr, c, cfgBaseURL, claims.UserID, email)
		c.JSON(http.StatusOK, gin.H{"message": "email updated — confirm it via the sent link"})
	}
}

// adminSetPassword sets the password and revokes every session and paired
// device as one security action: detached from the request so a dropped client
// cannot leave the old credentials alive after the password changed.
func adminSetPassword(parent context.Context, store *auth.Store, userID int, password string) error {
	ctx, cancel := httpshared.DetachedCtx(parent)
	defer cancel()
	if err := store.SetPassword(ctx, userID, password); err != nil {
		return err
	}
	return revokeAllAccess(ctx, store, userID)
}
