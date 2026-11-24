package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ErrPrimaryReadOnly is the JSON error an edge returns for writes while its
// database is a read-only standby (primary unreachable).
const ErrPrimaryReadOnly = "primary unavailable (read-only)"

// readOnlyAllowed are mutating routes that keep working while degraded: they
// act on this node's torrent client / player session, or tolerate a failed
// cache write, rather than needing durable shared state.
var readOnlyAllowed = []string{
	"/api/auth/refresh", // reissues an access token without rotation (see Refresh)
	"/api/stream/",
	"/api/diag/",
	"/api/user/incognito/",
	"/api/tmdb/",
}

// ReadOnlyGuard answers 503 to state-changing requests while degraded() is true,
// instead of letting them reach a store and fail with an opaque 500.
func ReadOnlyGuard(degraded func() bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if !degraded() || readOnlyPathAllowed(c.Request.URL.Path) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": ErrPrimaryReadOnly})
	}
}

func readOnlyPathAllowed(path string) bool {
	for _, p := range readOnlyAllowed {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}
