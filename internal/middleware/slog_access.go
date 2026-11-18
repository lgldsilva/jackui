package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"syscall"
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
		// Captured before c.Next(): handlers may rewrite the URL (spaFallback
		// sets Path="/" to serve index.html for deep links).
		path := logSafe(RedactToken(c.Request.URL.RequestURI()))
		c.Next()
		status := c.Writer.Status()
		attrs := []slog.Attr{
			slog.String("request_id", logSafe(RequestIDFrom(c))),
			slog.String("method", logSafe(c.Request.Method)),
			slog.String("route", logSafe(c.FullPath())),
			slog.String("path", path),
			slog.Int("status", status),
			slog.Float64("latency_ms", float64(time.Since(start).Microseconds())/1000),
			slog.String("client_ip", logSafe(c.ClientIP())),
			slog.Int("bytes", c.Writer.Size()),
		}
		if userID != nil {
			if id, ok := userID(c); ok {
				attrs = append(attrs, slog.Int("user_id", id))
			}
		}
		if errs := c.Errors.ByType(gin.ErrorTypePrivate).String(); errs != "" {
			attrs = append(attrs, slog.String("error", logSafe(RedactToken(errs))))
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
			err, _ := rec.(error)
			brokenPipe := isBrokenPipe(err)
			logger.LogAttrs(c.Request.Context(), slog.LevelError, "panic recovered",
				slog.String("request_id", logSafe(RequestIDFrom(c))),
				slog.String("method", logSafe(c.Request.Method)),
				slog.String("route", logSafe(c.FullPath())),
				slog.String("path", logSafe(RedactToken(c.Request.URL.Path))),
				slog.Any("panic", rec),
				slog.Bool("broken_pipe", brokenPipe),
				slog.String("stack", string(debug.Stack())),
			)
			if brokenPipe {
				// Dead connection: no status can be written (mirrors gin).
				_ = c.Error(err)
				c.Abort()
				return
			}
			c.AbortWithStatus(http.StatusInternalServerError)
		}()
		c.Next()
	}
}

// isBrokenPipe matches gin 1.12's recovery: a write to a closed connection
// or an http.ErrAbortHandler is not a server fault worth a 500.
func isBrokenPipe(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, http.ErrAbortHandler)
}

// logSafe strips CR/LF from request-derived values before they are
// logged. The JSON handler already escapes them; this also keeps a forged
// line out of any text sink the record may be copied to (log injection).
func logSafe(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", "")
}
