package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	ctxUserKey   = "auth.user"
	ctxClaimsKey = "auth.claims"
)

// Required is the Gin middleware that rejects requests without a valid Bearer token.
// On success, the parsed Claims are attached to the context and available via FromCtx.
func Required(tm *TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := extractToken(c)
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing access token"})
			return
		}
		claims, err := tm.ParseAccess(raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		c.Set(ctxClaimsKey, claims)
		c.Next()
	}
}

// Optional attaches claims if a valid token is present but never blocks.
// Useful for endpoints where behavior changes based on auth state (e.g., admin sees more).
func Optional(tm *TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := extractToken(c)
		if raw != "" {
			if claims, err := tm.ParseAccess(raw); err == nil {
				c.Set(ctxClaimsKey, claims)
			}
		}
		c.Next()
	}
}

// AdminOnly aborts with 403 unless the request was authenticated as an admin.
// Must be chained after Required.
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFromCtx(c)
		if !ok || claims.Role != RoleAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		c.Next()
	}
}

// ClaimsFromCtx retrieves the authenticated Claims previously attached by Required/Optional.
func ClaimsFromCtx(c *gin.Context) (*Claims, bool) {
	v, ok := c.Get(ctxClaimsKey)
	if !ok {
		return nil, false
	}
	claims, ok := v.(*Claims)
	return claims, ok
}

// UserIDFromCtx returns (userID, isAdmin, isAuthenticated). Use in handlers that filter by ownership.
func UserIDFromCtx(c *gin.Context) (int, bool, bool) {
	claims, ok := ClaimsFromCtx(c)
	if !ok {
		return 0, false, false
	}
	return claims.UserID, claims.Role == RoleAdmin, true
}

func extractToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	// Fallback: ?token=... in query (useful for `<video src>` requests where setting headers is impossible)
	if t := c.Query("token"); t != "" {
		return t
	}
	return ""
}
