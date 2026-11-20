package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// RefreshCookieName / RefreshCookiePath scope the httpOnly refresh cookie
// (issue #201): the web stops keeping the refresh token in localStorage and
// relies on the cookie; native clients ignore it and keep the body-based
// flow. The cookie only ever travels to /api/auth endpoints (refresh,
// logout) — never to media or API routes.
const (
	RefreshCookieName = "jackui_refresh"
	RefreshCookiePath = "/api/auth"
)

// refreshCookieSecure reports whether the request arrived over TLS — either
// terminated at this process or flagged by the reverse proxy.
func refreshCookieSecure(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	return strings.EqualFold(c.Request.Header.Get("X-Forwarded-Proto"), "https")
}

// cookieTTL is the cookie lifetime: it mirrors the refresh token's sliding
// window (24h normal / 30d remember) so an idle browser stops carrying it
// once the server-side session is gone anyway.
func cookieTTL(remember bool) time.Duration {
	if remember {
		return refreshTTLRemember
	}
	return refreshTTLNormal
}

// setRefreshCookie writes the httpOnly refresh cookie. SameSite=Strict: the
// cookie is only read by same-origin fetches to /api/auth, never by
// cross-site navigation.
func setRefreshCookie(c *gin.Context, token string, ttl time.Duration) {
	if token == "" {
		return
	}
	c.SetSameSite(http.SameSiteStrictMode)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    token,
		Path:     RefreshCookiePath,
		MaxAge:   int(ttl / time.Second),
		HttpOnly: true,
		Secure:   refreshCookieSecure(c),
		SameSite: http.SameSiteStrictMode,
	})
}

// clearRefreshCookie expires the refresh cookie. Used on logout (the token
// itself is revoked server-side separately).
func clearRefreshCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     RefreshCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   refreshCookieSecure(c),
		SameSite: http.SameSiteStrictMode,
	})
}

// refreshFromCookie returns the request's refresh cookie value, if any.
func refreshFromCookie(c *gin.Context) string {
	token, err := c.Cookie(RefreshCookieName)
	if err != nil {
		return ""
	}
	return token
}

// writeTokens emits the standard token bundle plus the httpOnly refresh
// cookie. Bodies are unchanged so native clients keep the body-based flow.
func writeTokens(c *gin.Context, resp tokenResp, remember bool) {
	setRefreshCookie(c, resp.Refresh, cookieTTL(remember))
	c.JSON(http.StatusOK, resp)
}
