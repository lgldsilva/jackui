package handlers

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
)

// errOAuthUsernameExhausted guards the (paranoid) auto-provision collision loop.
var errOAuthUsernameExhausted = errors.New("não foi possível derivar um username único")

// ─── Google OAuth login ("Entrar com Google") ───────────────────────────────
//
// Browser flow: GET /oauth/google/start → Google → GET /oauth/google/callback
// (matches an account by e-mail, same rule as the Gitea SSO) → 302 to the SPA
// carrying a single-use opaque code → POST /oauth/exchange → tokenResp.
// Tokens never ride in a URL that lands in history; the code does, but it's
// single-use, 5-minute TTL and worthless without the server.

// oauthErrorRedirect slugs — the SPA maps them to localized login messages.
const (
	oauthErrDisabled         = "disabled"
	oauthErrState            = "state"
	oauthErrExchange         = "exchange"
	oauthErrEmailUnverified  = "email_unverified"
	oauthErrDomainNotAllowed = "domain_not_allowed"
	oauthErrUnknownAccount   = "unknown_account"
	oauthErrAccountPending   = "account_pending"
	oauthErrAccountDisabled  = "account_disabled"
	oauthErrLoginFailed      = "login_failed"
)

// GoogleOAuthHandlers bundles the dependencies for the Google login routes.
type GoogleOAuthHandlers struct {
	store *auth.Store
	tm    *auth.TokenManager
	flow  *auth.OAuthFlowStore
	opts  auth.GoogleOptions
	eps   auth.GoogleEndpoints
}

// GoogleOAuth wires the handlers. endpoints is normally
// auth.DefaultGoogleEndpoints (injectable for tests).
func GoogleOAuth(store *auth.Store, tm *auth.TokenManager, cfg config.AuthOAuth, baseURL string, endpoints auth.GoogleEndpoints) *GoogleOAuthHandlers {
	opts := auth.GoogleOptions{
		ClientID:       cfg.ClientID,
		ClientSecret:   cfg.ClientSecret,
		RedirectURL:    cfg.RedirectURL,
		AutoProvision:  cfg.AutoProvision,
		AllowedDomains: cfg.AllowedDomains,
	}
	if opts.RedirectURL == "" && baseURL != "" {
		opts.RedirectURL = strings.TrimSuffix(baseURL, "/") + "/api/auth/oauth/google/callback"
	}
	return &GoogleOAuthHandlers{
		store: store,
		tm:    tm,
		flow:  auth.NewOAuthFlowStore(),
		opts:  opts,
		eps:   endpoints,
	}
}

// Ready reports whether the Google flow is fully configured.
func (h *GoogleOAuthHandlers) Ready() bool { return h.opts.Ready() }

// Providers handles GET /api/auth/oauth/providers — public feature-detect so
// the login page shows the Google button only when it would actually work.
func (h *GoogleOAuthHandlers) Providers() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"google": h.Ready()})
	}
}

// Start handles GET /api/auth/oauth/google/start — kicks off the OIDC
// round-trip. remember=0 (default 1) controls the sliding refresh window.
func (h *GoogleOAuthHandlers) Start() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !h.Ready() {
			oauthFail(c, h.spaLoginURL(oauthErrDisabled))
			return
		}
		remember := c.Query("remember") != "0"
		state, verifier, err := h.flow.NewState(remember)
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusInternalServerError, errTokenSigningFailed)
			return
		}
		target := h.eps.AuthorizeURL(h.opts, state, auth.PKCEChallenge(verifier))
		c.Redirect(http.StatusFound, target)
	}
}

// Callback handles GET /api/auth/oauth/google/callback — Google lands here with
// ?code&state (or ?error=access_denied). On success the browser is bounced to
// the SPA with a single-use exchange code; the actual tokens are issued by
// /oauth/exchange so MFA accounts can still be challenged.
func (h *GoogleOAuthHandlers) Callback() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !h.Ready() {
			oauthFail(c, h.spaLoginURL(oauthErrDisabled))
			return
		}
		if e := c.Query("error"); e != "" {
			// User denied consent / Google-side failure — treat as a cancelled
			// login, not an exception worth logging beyond a line.
			log.Printf("Google OAuth: provider returned error %q", httpshared.SanitizeForLog(e))
			oauthFail(c, h.spaLoginURL(oauthErrLoginFailed))
			return
		}
		code := c.Query("code")
		state := c.Query("state")
		if code == "" || state == "" {
			oauthFail(c, h.spaLoginURL(oauthErrState))
			return
		}
		verifier, remember, ok := h.flow.ConsumeState(state)
		if !ok {
			oauthFail(c, h.spaLoginURL(oauthErrState))
			return
		}
		info, err := auth.ExchangeGoogleCode(c.Request.Context(), h.eps, h.opts, code, verifier)
		if err != nil {
			log.Printf("Google OAuth: token/userinfo exchange failed: %v", err)
			oauthFail(c, h.spaLoginURL(oauthErrExchange))
			return
		}
		if !info.EmailVerified {
			oauthFail(c, h.spaLoginURL(oauthErrEmailUnverified))
			return
		}
		if !h.opts.DomainAllowed(info.Email) {
			oauthFail(c, h.spaLoginURL(oauthErrDomainNotAllowed))
			return
		}

		user, err := h.store.GetUserByEmail(info.Email)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		if user == nil {
			if !h.opts.AutoProvision {
				// Same linking contract as Gitea: no account with this e-mail →
				// refuse (an admin creates/invites the account first).
				log.Printf("Google OAuth: no JackUI account for e-mail of user id 0 (refused; auto_provision=off)")
				oauthFail(c, h.spaLoginURL(oauthErrUnknownAccount))
				return
			}
			if user, err = h.provision(info.Email); err != nil {
				httpshared.RespondError(c, http.StatusInternalServerError, err)
				return
			}
		}

		switch user.Status {
		case auth.StatusPending:
			oauthFail(c, h.spaLoginURL(oauthErrAccountPending))
			return
		case auth.StatusDisabled:
			oauthFail(c, h.spaLoginURL(oauthErrAccountDisabled))
			return
		}

		exchangeCode, err := h.flow.SavePending(user.ID, remember)
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusInternalServerError, errTokenSigningFailed)
			return
		}
		c.Redirect(http.StatusFound, h.spaCallbackURL(exchangeCode))
	}
}

// Exchange handles POST /api/auth/oauth/exchange — body {code, totp?}. Issues
// the real token pair. Accounts with MFA get challenged here (the SPA shows a
// TOTP prompt and re-posts), mirroring the password login's mfaRequired flow.
func (h *GoogleOAuthHandlers) Exchange() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Code string `json:"code"`
			Totp string `json:"totp"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "código de troca obrigatório")
			return
		}
		userID, remember, ok := h.flow.PeekPending(req.Code)
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, "código de troca inválido ou expirado")
			return
		}
		user, err := h.store.GetUserByID(userID)
		if err != nil || user == nil {
			h.flow.ConsumePending(req.Code)
			httpshared.RespondErrorMessage(c, http.StatusUnauthorized, "sessão de login inválida")
			return
		}
		// Status may have changed between callback and exchange.
		if respondIfInactive(c, user.Status) {
			h.flow.ConsumePending(req.Code)
			return
		}
		if user.MfaEnabled {
			if req.Totp == "" {
				httpshared.RespondErrorMessageFields(c, http.StatusUnauthorized, "código MFA obrigatório", gin.H{"mfaRequired": true})
				return
			}
			secret, _, _ := h.store.GetTOTPSecret(user.ID)
			if !auth.ValidateTOTP(secret, req.Totp) && !h.store.ConsumeBackupCode(user.ID, req.Totp) {
				httpshared.RespondErrorMessageFields(c, http.StatusUnauthorized, "código MFA inválido", gin.H{"mfaRequired": true})
				return
			}
		}
		// Single-use: consumed only on the success path — a wrong TOTP code can
		// be retried while the pending code is still alive.
		h.flow.ConsumePending(req.Code)
		resp, err := issueTokens(h.store, h.tm, user, remember, c.Request.UserAgent(), c.ClientIP())
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusInternalServerError, errTokenSigningFailed)
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}

// provision auto-creates an account for a verified Google e-mail. Username is
// derived from the local part (sanitized, made unique); the random password is
// unusable until the user sets one via reset — Google remains the login path.
func (h *GoogleOAuthHandlers) provision(email string) (*auth.User, error) {
	base := deriveUsername(email)
	username := base
	for i := 2; ; i++ {
		exists, err := h.store.Exists(username, "")
		if err != nil {
			return nil, err
		}
		if !exists {
			break
		}
		if i > 100 {
			return nil, errOAuthUsernameExhausted
		}
		username = base + "-" + strconv.Itoa(i)
	}
	password, err := auth.RandomToken(24)
	if err != nil {
		return nil, err
	}
	id, err := h.store.CreateUserFull(username, email, password, auth.RoleUser, auth.StatusActive)
	if err != nil {
		return nil, err
	}
	log.Printf("Google OAuth: auto-provisioned account id=%d username=%q", id, httpshared.SanitizeForLog(username))
	return h.store.GetUserByID(id)
}

// spaOrigin strips the redirect_uri pattern back to the SPA origin ("" →
// same-origin relative paths when no BaseURL is configured).
func (h *GoogleOAuthHandlers) spaOrigin() string {
	base := strings.TrimSuffix(h.opts.RedirectURL, "/api/auth/oauth/google/callback")
	if base == "" || base == "/" {
		return ""
	}
	return strings.TrimSuffix(base, "/")
}

func (h *GoogleOAuthHandlers) spaLoginURL(errSlug string) string {
	return h.spaOrigin() + "/login?oauthError=" + url.QueryEscape(errSlug)
}

func (h *GoogleOAuthHandlers) spaCallbackURL(exchangeCode string) string {
	return h.spaOrigin() + "/auth/google/callback?code=" + url.QueryEscape(exchangeCode)
}

// oauthFail sends the browser to the login page with an error slug. When the
// caller isn't a browser redirect context (API misuse), answer JSON instead.
func oauthFail(c *gin.Context, redirectURL string) {
	if c.Request.Method == http.MethodGet {
		c.Redirect(http.StatusFound, redirectURL)
		return
	}
	httpshared.RespondErrorMessage(c, http.StatusUnauthorized, "oauth login failed")
}

// deriveUsername turns "First.Last@Sub.Domain" into "first.last" — a sane,
// unique-able username seed from an e-mail local part.
func deriveUsername(email string) string {
	at := strings.Index(email, "@")
	local := strings.ToLower(email)
	if at >= 0 {
		local = strings.ToLower(email[:at])
	}
	var b strings.Builder
	for _, r := range local {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), ".-_")
	if out == "" {
		out = "user"
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}
