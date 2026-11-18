package main

import (
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/logging"
	"github.com/lgldsilva/jackui/internal/middleware"
)

// setupLogger installs the process logger (JACKUI_LOG_FORMAT=json → one JSON
// object per line for slog, stdlib log, gin and anacrolix; anything else →
// the historical text output).
func setupLogger() {
	h := logging.Setup(os.Getenv("JACKUI_LOG_FORMAT"), os.Stdout)
	if logging.JSONEnabled() {
		// Safety net for anything gin still prints on its own writers
		// (debug-mode banners, third-party middleware using DefaultWriter).
		w := logging.NewLineWriter(h, "gin")
		gin.DefaultWriter = w
		gin.DefaultErrorWriter = w
	}
}

// httpLogMiddlewares returns the request-id, access-log and recovery
// middlewares. Text mode keeps the exact historical access-log line and
// gin.Recovery; JSON mode swaps both for slog-based equivalents.
func httpLogMiddlewares(jsonMode bool) []gin.HandlerFunc {
	if !jsonMode {
		// Custom formatter instead of gin.Logger(): media routes authenticate via
		// ?token=<JWT> (<video> can't send headers), and the default access log was
		// writing those JWTs verbatim into `docker logs`.
		return []gin.HandlerFunc{
			middleware.RequestID(),
			gin.LoggerWithFormatter(middleware.RedactingLogFormatter),
			gin.Recovery(),
		}
	}
	logger := slog.Default()
	return []gin.HandlerFunc{
		middleware.RequestID(),
		middleware.SlogAccessLog(logger, accessLogUserID),
		middleware.SlogRecovery(logger),
	}
}

func accessLogUserID(c *gin.Context) (int, bool) {
	id, _, ok := auth.UserIDFromCtx(c)
	return id, ok
}
