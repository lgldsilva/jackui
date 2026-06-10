package handlers

import (
	"fmt"
	"log"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
)

// diagTokenRe redacts media/JWT tokens out of client diagnostics before they
// hit the server log. The player reports stream URLs (src/currentSrc) that
// carry ?token=<JWT> — logging them verbatim would defeat the whole reason
// ?token= is restricted to media paths (keeping credentials out of logs).
var diagTokenRe = regexp.MustCompile(`(token=)[^&"'\s\]}]+`)

// ClientLogPayload is the shape the browser sends.
// `data` is free-form — typically a video-diagnostic snapshot.
type ClientLogPayload struct {
	Level string         `json:"level" binding:"required"` // "info" | "warn" | "error"
	Tag   string         `json:"tag"`                      // e.g. "player"
	Msg   string         `json:"msg" binding:"required"`
	Data  map[string]any `json:"data"`
}

// ClientLog accepts diagnostic messages from the browser and writes them to the
// server log. Useful for debugging Safari-only HEVC/HLS path issues where the
// user can't easily share devtools output.
//
// Trade-off: this is an authenticated firehose — a malicious client could spam
// logs. Body size limited and structured to keep parse cost bounded.
func ClientLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Reject anything obviously huge (>16KiB JSON is plenty for diagnostics).
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)

		var p ClientLogPayload
		if err := c.ShouldBindJSON(&p); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		tag := p.Tag
		if tag == "" {
			tag = "client"
		}
		// Use the standard log package — appears in `docker logs jackui`
		// alongside Gin's request log. Single-line so it's grep-friendly.
		line := fmt.Sprintf("[%s] user=%d level=%s msg=%q data=%v", tag, userID, p.Level, p.Msg, p.Data)
		log.Print(diagTokenRe.ReplaceAllString(line, "${1}REDACTED"))
		c.Status(http.StatusNoContent)
	}
}
