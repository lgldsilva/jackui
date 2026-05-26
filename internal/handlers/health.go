package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/history"
	"github.com/luizg/jackui/internal/jackett"
)

// Health handles GET /healthz — liveness check. Fast, no external deps.
// Returns 200 as long as the JackUI process and DB are alive.
func Health(store *history.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		dbStatus := "ok"
		if store == nil {
			dbStatus = "disabled"
		} else if _, err := store.RecentEntries(1, 0, true); err != nil {
			dbStatus = "down: " + err.Error()
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "degraded", "db": dbStatus,
				"time": time.Now().UTC().Format(time.RFC3339),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status": "ok", "db": dbStatus,
			"time": time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// Status handles GET /api/status — full readiness check including Jackett.
// Uses a short timeout so it never blocks the UI.
func Status(client *jackett.Client, store *history.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		out := gin.H{
			"status":  "ok",
			"jackett": "ok",
			"db":      "ok",
			"time":    time.Now().UTC().Format(time.RFC3339),
		}

		// Jackett ping with short timeout
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		errCh := make(chan error, 1)
		go func() { errCh <- client.TestConnection() }()
		select {
		case err := <-errCh:
			if err != nil {
				out["jackett"] = "down: " + err.Error()
				out["status"] = "degraded"
			}
		case <-ctx.Done():
			out["jackett"] = "timeout (5s)"
			out["status"] = "degraded"
		}

		if store == nil {
			out["db"] = "disabled"
		} else if _, err := store.RecentEntries(1, 0, true); err != nil {
			out["db"] = "down: " + err.Error()
			out["status"] = "degraded"
		}

		code := http.StatusOK
		if out["status"] == "degraded" {
			code = http.StatusServiceUnavailable
		}
		c.JSON(code, out)
	}
}

