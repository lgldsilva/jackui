package handlers

import (
	"encoding/base64"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
)

// Passkey (WebAuthn) handlers. A ceremony is two requests: a "begin" that mints
// the challenge options + an opaque session id, and a "finish" that posts the
// authenticator's raw response (in the request BODY, which go-webauthn parses
// directly) plus the session id (carried in ?session= so the body stays the raw
// credential). Registration is authenticated; login is public and issues tokens.

// PasskeyRegisterBegin handles POST /api/auth/passkey/register/begin (authed).
func PasskeyRegisterBegin(store *auth.Store, wa *auth.WAManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		if wa == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrPasskeysNotConfigF})
			return
		}
		creds, _ := store.Credentials(claims.UserID)
		opts, session, err := wa.BeginRegister(claims.UserID, claims.Username, creds)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"options": opts, "session": session})
	}
}

// PasskeyRegisterFinish handles POST /api/auth/passkey/register/finish?session=ID (authed).
func PasskeyRegisterFinish(store *auth.Store, wa *auth.WAManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		if wa == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrPasskeysNotConfig})
			return
		}
		creds, _ := store.Credentials(claims.UserID)
		cred, err := wa.FinishRegister(claims.UserID, claims.Username, creds, c.Query("session"), c.Request)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := store.AddCredential(claims.UserID, cred); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "passkey adicionada"})
	}
}

// PasskeyLoginBegin handles POST /api/auth/passkey/login/begin (public) — body {username}.
func PasskeyLoginBegin(store *auth.Store, wa *auth.WAManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if wa == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrPasskeysNotConfig})
			return
		}
		var req struct {
			Username string `json:"username"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "username obrigatório"})
			return
		}
		user, err := store.GetUserByUsername(req.Username)
		// Neutral response shape, but a passkey login needs the credential list to
		// build the assertion challenge — so an unknown user / no-passkey can't be
		// fully hidden here. Return a generic error without leaking which case it is.
		if err != nil || user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "passkey indisponível para este usuário"})
			return
		}
		creds, _ := store.Credentials(user.ID)
		if len(creds) == 0 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "passkey indisponível para este usuário"})
			return
		}
		opts, session, err := wa.BeginLogin(user.ID, user.Username, creds)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"options": opts, "session": session, "userId": user.ID})
	}
}

// PasskeyLoginFinish handles POST /api/auth/passkey/login/finish?session=ID&uid=N (public).
func PasskeyLoginFinish(store *auth.Store, tm *auth.TokenManager, wa *auth.WAManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if wa == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrPasskeysNotConfig})
			return
		}
		// The assertion JSON is the body go-webauthn parses (we must NOT consume it
		// with ShouldBindJSON), so the username + flags ride in the query string.
		user, err := store.GetUserByUsername(c.Query("username"))
		if err != nil || user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "falha na autenticação por passkey"})
			return
		}
		switch user.Status {
		case auth.StatusPending:
			c.JSON(http.StatusForbidden, gin.H{"error": "conta aguardando aprovação", "status": "pending"})
			return
		case auth.StatusDisabled:
			c.JSON(http.StatusForbidden, gin.H{"error": "conta desabilitada", "status": "disabled"})
			return
		}
		creds, _ := store.Credentials(user.ID)
		cred, err := wa.FinishLogin(user.ID, user.Username, creds, c.Query("session"), c.Request)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "falha na autenticação por passkey"})
			return
		}
		// Persist the advanced sign counter (clone-detection state).
		_ = store.UpdateCredential(cred)

		access, exp, err := tm.SignAccess(user)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": errTokenSigningFailed})
			return
		}
		ttl := refreshTTLNormal
		if c.Query("remember") == "1" {
			ttl = refreshTTLRemember
		}
		refresh, err := store.CreateRefreshToken(user.ID, ttl, ttl == refreshTTLRemember)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, tokenResp{Access: access, Refresh: refresh, ExpiresAt: exp, User: user})
	}
}

// PasskeyList handles GET /api/auth/passkey (authed) — returns the user's
// registered passkeys (base64url ids only; the public keys aren't exposed).
func PasskeyList(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		creds, _ := store.Credentials(claims.UserID)
		out := make([]gin.H, 0, len(creds))
		for _, cr := range creds {
			out = append(out, gin.H{"id": base64.RawURLEncoding.EncodeToString(cr.ID)})
		}
		c.JSON(http.StatusOK, gin.H{"passkeys": out})
	}
}

// PasskeyDelete handles DELETE /api/auth/passkey/:id (authed) — id is base64url.
func PasskeyDelete(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.ClaimsFromCtx(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errNotAuthenticated})
			return
		}
		if err := store.DeleteCredential(claims.UserID, c.Param("id")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "passkey removida"})
	}
}
