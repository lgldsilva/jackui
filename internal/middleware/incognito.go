// Package middleware holds cross-cutting Gin middlewares that don't fit a
// single feature package. Today it's just the incognito flag — handlers that
// would normally write search history or library entries consult IsIncognito(c)
// at the top and skip the write silently, while still returning the same HTTP
// response so the frontend UX stays fluid.
package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	headerName = "X-JackUI-Incognito"
	ctxKey     = "incognito"
)

// Incognito reads X-JackUI-Incognito from the request and tags the context
// when it's "1" / "true" (case-insensitive). Mount on the /api group.
//
// EventSource can't set headers, so SSE callers (/api/search/stream) instead
// pass ?incognito=1 in the query — accepted here as a fallback for the same
// reason the auth middleware accepts ?token=.
func Incognito() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isTruthy(c.GetHeader(headerName)) || isTruthy(c.Query("incognito")) {
			c.Set(ctxKey, true)
		}
		c.Next()
	}
}

// IsIncognito returns true when the current request was marked incognito by
// the middleware. Safe to call from any handler — defaults to false when the
// middleware isn't installed (e.g. unit tests bypassing the router).
func IsIncognito(c *gin.Context) bool {
	return c.GetBool(ctxKey)
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
