package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// DefaultJSONBodyCap bounds request bodies on the JSON API. Real payloads are
// KB-scale (magnets, settings, playlist edits); 2MB is generous headroom.
const DefaultJSONBodyCap = 2 << 20

// BodyLimit wraps every request body in http.MaxBytesReader so a handler can't
// be forced to buffer an unbounded body. A request declaring an oversized
// Content-Length is rejected upfront with 413; a chunked body that exceeds the
// cap mid-read fails at the handler's bind with the MaxBytesReader error.
//
// exempt reports whether a path opts out (uploads with their own bound, etc.) —
// keep the exemption list at the registration site so the exceptions stay
// visible next to the routes they protect.
func BodyLimit(maxBytes int64, exempt func(path string) bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if exempt != nil && exempt(c.Request.URL.Path) {
			c.Next()
			return
		}
		if c.Request.ContentLength > maxBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}
