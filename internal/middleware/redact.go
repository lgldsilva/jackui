package middleware

import (
	"fmt"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
)

// tokenRe matches `token=<value>` in URLs and free-form diagnostic text.
// Media routes authenticate via ?token=<JWT> because <video>/<track> can't
// send headers — so any place that logs a URL must mask the value or the JWT
// ends up greppable in `docker logs`. The character class stops at query
// separators AND at quote/bracket/space so it also works inside JSON-ish
// diagnostic payloads (see handlers.ClientLog).
var tokenRe = regexp.MustCompile(`(token=)[^&"'\s\]}]+`)

// RedactToken masks every token=... credential in s with token=REDACTED.
func RedactToken(s string) string {
	return tokenRe.ReplaceAllString(s, "${1}REDACTED")
}

// RedactingLogFormatter is gin's default access-log line (timestamp, status,
// latency, client IP, method, path+query) minus terminal colors, with
// credential query values masked via RedactToken. Wire it with
// gin.LoggerWithFormatter instead of gin.Logger(): the default logger printed
// media URLs verbatim, leaking the ?token= JWT of every stream/subtitle
// request into the server log (verified in production).
func RedactingLogFormatter(p gin.LogFormatterParams) string {
	if p.Latency > time.Minute {
		// Same truncation as gin's default formatter — keeps the column readable.
		p.Latency = p.Latency.Truncate(time.Second)
	}
	return fmt.Sprintf("[GIN] %s | %3d | %13v | %15s | %-7s %#v\n%s",
		p.TimeStamp.Format("2006/01/02 - 15:04:05"),
		p.StatusCode,
		p.Latency,
		p.ClientIP,
		p.Method,
		RedactToken(p.Path),
		RedactToken(p.ErrorMessage),
	)
}
