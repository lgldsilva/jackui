package middleware

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// UserIDFunc extracts the authenticated user id from the gin context
// (auth.UserIDFromCtx adapter); ok=false when the request is anonymous.
type UserIDFunc func(c *gin.Context) (id int, ok bool)

// SlogAccessLog is the JSON-mode replacement for gin's access log: one
// structured record per request on logger, with the route template (low
// cardinality), the token-redacted raw path, status, latency and request id.
// 5xx logs at ERROR, 4xx at WARN, the rest at INFO.
func SlogAccessLog(logger *slog.Logger, userID UserIDFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		status := c.Writer.Status()
		attrs := []slog.Attr{
			slog.String("request_id", RequestIDFrom(c)),
			slog.String("method", c.Request.Method),
			slog.String("route", c.FullPath()),
			slog.String("path", RedactToken(c.Request.URL.RequestURI())),
			slog.Int("status", status),
			slog.Float64("latency_ms", float64(time.Since(start).Microseconds())/1000),
			slog.String("client_ip", c.ClientIP()),
			slog.Int("bytes", c.Writer.Size()),
		}
		if userID != nil {
			if id, ok := userID(c); ok {
				attrs = append(attrs, slog.Int("user_id", id))
			}
		}
		if errs := c.Errors.ByType(gin.ErrorTypePrivate).String(); errs != "" {
			attrs = append(attrs, slog.String("error", RedactToken(errs)))
		}
		logger.LogAttrs(c.Request.Context(), accessLevel(status), "http request", attrs...)
	}
}

func accessLevel(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// SlogRecovery is the JSON-mode replacement for gin.Recovery: it logs the
// panic value and stack as one ERROR record (gin's version writes a
// multi-line text dump) and answers 500. A client that hung up (broken pipe /
// connection reset) gets no response write, mirroring gin.
func SlogRecovery(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			brokenPipe := isBrokenPipe(rec)
			logger.LogAttrs(c.Request.Context(), slog.LevelError, "panic recovered",
				slog.String("request_id", RequestIDFrom(c)),
				slog.String("method", c.Request.Method),
				slog.String("route", c.FullPath()),
				slog.String("path", RedactToken(c.Request.URL.Path)),
				slog.Any("panic", rec),
				slog.Bool("broken_pipe", brokenPipe),
				slog.String("stack", string(debug.Stack())),
			)
			if brokenPipe {
				c.Abort()
				return
			}
			c.AbortWithStatus(http.StatusInternalServerError)
		}()
		c.Next()
	}
}

func isBrokenPipe(rec any) bool {
	err, ok := rec.(error)
	if !ok {
		return false
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return false
	}
	var sysErr *os.SyscallError
	if !errors.As(opErr, &sysErr) {
		return false
	}
	msg := strings.ToLower(sysErr.Error())
	return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset by peer")
}
