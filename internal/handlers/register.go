package handlers

import (
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/mailer"
)

const (
	inviteTTL = 7 * 24 * time.Hour
	verifyTTL = 24 * time.Hour
	resetTTL  = 1 * time.Hour
)

// baseURL resolves the public base URL for building email links from trusted
// configuration only. Request headers and host data are attacker-controlled.
func baseURL(_ *gin.Context, configured string) string {
	return strings.TrimRight(strings.TrimSpace(configured), "/")
}

// notify sends an email link, or — when SMTP is off — logs it so an admin (or a
// local dev) can relay/copy it. Always best-effort; never blocks the response.
func notify(mlr *mailer.Mailer, to, subject, intro, link string) {
	body := fmt.Sprintf(
		`<p>%s</p><p><a href="%s">%s</a></p><p style="color:#888;font-size:12px">If you did not request this, ignore this email.</p>`,
		html.EscapeString(intro), html.EscapeString(link), html.EscapeString(link),
	)
	if mlr != nil && mlr.Enabled() && to != "" {
		if err := mlr.Send(to, subject, body); err != nil {
			log.Printf("auth: email to %s failed (%v) — link: %s", httpshared.SanitizeForLog(to), err, httpshared.SanitizeForLog(link))
		}
		return
	}
	// No SMTP: surface the link in the logs so it can be relayed manually.
	log.Printf("auth: [no-smtp] %s for %s → %s", httpshared.SanitizeForLog(subject), httpshared.SanitizeForLog(to), httpshared.SanitizeForLog(link))
}

type registerReq struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Invite   string `json:"invite"`
}

// Register handles POST /api/auth/register (public). Invite token → active
// account; no invite → pending (awaits admin approval). Either way a
// confirmation email is sent. Username/email must be free.
func Register(store *auth.Store, mlr *mailer.Mailer, cfgBaseURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		registerHandler(c, store, mlr, cfgBaseURL)
	}
}

func registerHandler(c *gin.Context, store *auth.Store, mlr *mailer.Mailer, cfgBaseURL string) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpshared.RespondErrorMessage(c, http.StatusBadRequest, httpshared.ErrInvalidData)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Username == "" || req.Email == "" || len(req.Password) < 6 {
		httpshared.RespondErrorMessage(c, http.StatusBadRequest, "username, email and password (≥6) are required")
		return
	}
	taken, err := store.Exists(req.Username, req.Email)
	if err != nil {
		httpshared.RespondError(c, http.StatusInternalServerError, err)
		return
	}
	if taken {
		httpshared.RespondErrorMessage(c, http.StatusConflict, "username or email already registered")
		return
	}

	status, invited, ierr := resolveInviteStatus(store, req.Invite)
	if ierr != nil {
		httpshared.RespondError(c, http.StatusBadRequest, ierr)
		return
	}

	uid, err := store.CreateUserFull(req.Username, req.Email, req.Password, auth.RoleUser, status)
	if err != nil {
		httpshared.RespondError(c, http.StatusInternalServerError, err)
		return
	}

	sendVerifyEmail(store, mlr, c, cfgBaseURL, uid, req.Email)

	msg := "Account created. Confirm your email and wait for an admin's approval."
	if invited {
		msg = "Account created. Confirm your email — you can sign in right away."
	}
	c.JSON(http.StatusOK, gin.H{"status": string(status), "invited": invited, "message": msg})
}

func resolveInviteStatus(store *auth.Store, inviteToken string) (auth.Status, bool, error) {
	if inviteToken == "" {
		return auth.StatusPending, false, nil
	}
	if _, terr := store.ConsumeToken(inviteToken, auth.TokenInvite); terr != nil {
		return auth.StatusPending, false, fmt.Errorf("invalid or expired invite")
	}
	return auth.StatusActive, true, nil
}

func sendVerifyEmail(store *auth.Store, mlr *mailer.Mailer, c *gin.Context, cfgBaseURL string, id int, email string) {
	base := baseURL(c, cfgBaseURL)
	if base == "" {
		return
	}
	if tok, terr := store.CreateToken(auth.TokenVerifyEmail, id, email, verifyTTL); terr == nil {
		link := base + "/verify-email?token=" + tok
		notify(mlr, email, "JackUI — confirm your email", "Confirm your email to finish signing up:", link)
	}
}

// Invite handles POST /api/auth/invite (admin). Generates an invite link; emails
// it when an address is given + SMTP is on. Always returns the link so the admin
// can share it manually.
func Invite(store *auth.Store, mlr *mailer.Mailer, cfgBaseURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Email string `json:"email"`
		}
		_ = c.ShouldBindJSON(&req)
		req.Email = strings.TrimSpace(strings.ToLower(req.Email))
		base := baseURL(c, cfgBaseURL)
		if base == "" {
			httpshared.RespondErrorMessage(c, http.StatusInternalServerError, "public base URL not configured")
			return
		}
		tok, err := store.CreateToken(auth.TokenInvite, 0, req.Email, inviteTTL)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		link := base + "/register?invite=" + tok
		if req.Email != "" {
			notify(mlr, req.Email, "JackUI — invite", "You have been invited to JackUI. Create your account:", link)
		}
		c.JSON(http.StatusOK, gin.H{"link": link})
	}
}

// VerifyEmail handles POST /api/auth/verify-email (public). Confirms the address.
func VerifyEmail(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Token string `json:"token"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Token == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "token required")
			return
		}
		ti, err := store.ConsumeToken(req.Token, auth.TokenVerifyEmail)
		if err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		if err := store.SetEmailVerified(ti.UserID, ""); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "email confirmed"})
	}
}

// Forgot handles POST /api/auth/forgot (public). Emails a reset link if the
// address exists. ALWAYS returns a neutral 200 — never reveals whether the email
// is registered.
func Forgot(store *auth.Store, mlr *mailer.Mailer, cfgBaseURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Email string `json:"email"`
		}
		_ = c.ShouldBindJSON(&req)
		email := strings.TrimSpace(strings.ToLower(req.Email))
		if u, _ := store.GetUserByEmail(email); u != nil {
			base := baseURL(c, cfgBaseURL)
			if base != "" {
				if tok, terr := store.CreateToken(auth.TokenResetPassword, u.ID, email, resetTTL); terr == nil {
					link := base + "/reset-password?token=" + tok
					notify(mlr, email, "JackUI — password recovery", "To reset your password, go to:", link)
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{"message": "If the email is registered, we have sent a recovery link."})
	}
}

// Reset handles POST /api/auth/reset (public). Consumes a reset token + sets the
// new password.
func Reset(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Token == "" || len(req.Password) < 6 {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "token and new password (≥6) are required")
			return
		}
		ti, err := store.ConsumeToken(req.Token, auth.TokenResetPassword)
		if err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		if err := store.SetPassword(ti.UserID, req.Password); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "password reset"})
	}
}
