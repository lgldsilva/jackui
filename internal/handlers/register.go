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
	"github.com/lgldsilva/jackui/internal/mailer"
)

const (
	inviteTTL = 7 * 24 * time.Hour
	verifyTTL = 24 * time.Hour
	resetTTL  = 1 * time.Hour
)

// baseURL resolves the public base URL for building email links: the configured
// value wins; otherwise reconstruct it from the request (origin/host).
func baseURL(c *gin.Context, configured string) string {
	if configured != "" {
		return strings.TrimRight(configured, "/")
	}
	if o := c.GetHeader("Origin"); o != "" {
		return strings.TrimRight(o, "/")
	}
	scheme := "https"
	if c.Request.TLS == nil && c.GetHeader("X-Forwarded-Proto") == "" {
		scheme = "http"
	}
	return scheme + "://" + c.Request.Host
}

// notify sends an email link, or — when SMTP is off — logs it so an admin (or a
// local dev) can relay/copy it. Always best-effort; never blocks the response.
func notify(mlr *mailer.Mailer, to, subject, intro, link string) {
	body := fmt.Sprintf(
		`<p>%s</p><p><a href="%s">%s</a></p><p style="color:#888;font-size:12px">Se você não solicitou, ignore este e-mail.</p>`,
		html.EscapeString(intro), html.EscapeString(link), html.EscapeString(link),
	)
	if mlr != nil && mlr.Enabled() && to != "" {
		if err := mlr.Send(to, subject, body); err != nil {
			log.Printf("auth: email to %s failed (%v) — link: %s", to, err, link)
		}
		return
	}
	// No SMTP: surface the link in the logs so it can be relayed manually.
	log.Printf("auth: [no-smtp] %s for %s → %s", subject, to, link)
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
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidData})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Username == "" || req.Email == "" || len(req.Password) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "usuário, e-mail e senha (≥6) são obrigatórios"})
		return
	}
	taken, err := store.Exists(req.Username, req.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if taken {
		c.JSON(http.StatusConflict, gin.H{"error": "usuário ou e-mail já cadastrado"})
		return
	}

	status, invited, ierr := resolveInviteStatus(store, req.Invite)
	if ierr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": ierr.Error()})
		return
	}

	uid, err := store.CreateUserFull(req.Username, req.Email, req.Password, auth.RoleUser, status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	sendVerifyEmail(store, mlr, c, cfgBaseURL, uid, req.Email)

	msg := "Cadastro criado. Confirme seu e-mail e aguarde a aprovação de um admin."
	if invited {
		msg = "Cadastro criado. Confirme seu e-mail — você já pode entrar."
	}
	c.JSON(http.StatusOK, gin.H{"status": string(status), "invited": invited, "message": msg})
}

func resolveInviteStatus(store *auth.Store, inviteToken string) (auth.Status, bool, error) {
	if inviteToken == "" {
		return auth.StatusPending, false, nil
	}
	if _, terr := store.ConsumeToken(inviteToken, auth.TokenInvite); terr != nil {
		return auth.StatusPending, false, fmt.Errorf("convite inválido ou expirado")
	}
	return auth.StatusActive, true, nil
}

func sendVerifyEmail(store *auth.Store, mlr *mailer.Mailer, c *gin.Context, cfgBaseURL string, id int, email string) {
	if tok, terr := store.CreateToken(auth.TokenVerifyEmail, id, email, verifyTTL); terr == nil {
		link := baseURL(c, cfgBaseURL) + "/verify-email?token=" + tok
		notify(mlr, email, "JackUI — confirme seu e-mail", "Confirme seu e-mail para concluir o cadastro:", link)
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
		tok, err := store.CreateToken(auth.TokenInvite, 0, req.Email, inviteTTL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		link := baseURL(c, cfgBaseURL) + "/register?invite=" + tok
		if req.Email != "" {
			notify(mlr, req.Email, "JackUI — convite", "Você foi convidado para o JackUI. Crie sua conta:", link)
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "token obrigatório"})
			return
		}
		ti, err := store.ConsumeToken(req.Token, auth.TokenVerifyEmail)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := store.SetEmailVerified(ti.UserID, ""); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "e-mail confirmado"})
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
			if tok, terr := store.CreateToken(auth.TokenResetPassword, u.ID, email, resetTTL); terr == nil {
				link := baseURL(c, cfgBaseURL) + "/reset-password?token=" + tok
				notify(mlr, email, "JackUI — recuperar senha", "Para redefinir sua senha, acesse:", link)
			}
		}
		c.JSON(http.StatusOK, gin.H{"message": "Se o e-mail estiver cadastrado, enviamos um link de recuperação."})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "token e nova senha (≥6) obrigatórios"})
			return
		}
		ti, err := store.ConsumeToken(req.Token, auth.TokenResetPassword)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := store.SetPassword(ti.UserID, req.Password); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "senha redefinida"})
	}
}
