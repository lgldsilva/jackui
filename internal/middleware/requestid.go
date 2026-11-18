package middleware

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/logging"
)

// HeaderRequestID is the correlation header read from the client/proxy and
// echoed on every response.
const HeaderRequestID = "X-Request-ID"

// ctxRequestIDKey is the gin context key holding the request id.
const ctxRequestIDKey = "request_id"

const maxRequestIDLen = 64

// RequestID assigns each request a correlation id: an incoming X-Request-ID
// is reused only when it is a short token of [A-Za-z0-9._:-] (anything else —
// overlong, control chars, CR/LF log-injection attempts — is replaced by a
// fresh random id). The id is echoed in the response header, stored on the
// gin context and on the request context (logging.RequestID) so handlers
// using slog.*Context get it attached automatically.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(HeaderRequestID)
		if !validRequestID(id) {
			id = newRequestID()
		}
		c.Set(ctxRequestIDKey, id)
		c.Request = c.Request.WithContext(logging.WithRequestID(c.Request.Context(), id))
		c.Header(HeaderRequestID, id)
		c.Next()
	}
}

// RequestIDFrom returns the id assigned by RequestID, or "".
func RequestIDFrom(c *gin.Context) string {
	return c.GetString(ctxRequestIDKey)
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		if !requestIDChar(id[i]) {
			return false
		}
	}
	return true
}

func requestIDChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '-', b == '_', b == '.', b == ':':
		return true
	default:
		return false
	}
}

func newRequestID() string {
	var b [16]byte
	// crypto/rand.Read never returns an error on supported platforms (Go 1.24+).
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
