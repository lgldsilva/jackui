package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
)

// RateLimit returns a gin middleware that enforces per-IP rate limits using the
// provided limiter. When a request exceeds the limit the middleware responds
// with 429 Too Many Requests and a Retry-After header. It only applies to the
// wrapped routes (use per-group or per-route).
func RateLimit(limiter *auth.IPRateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		allowed, retryAfter := limiter.Allow(ip)
		if !allowed {
			secs := int(retryAfter.Seconds())
			if secs < 1 {
				secs = 1
			}
			c.Header("Retry-After", strconv.Itoa(secs))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "too many requests",
				"retry_after": secs,
			})
			return
		}
		c.Next()
	}
}
