package handlers

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/version"
)

const downPrefix = "down: "

// healthDBTimeout bounds the DB probe of the health endpoints. Docker polls
// /healthz every 30s with a 10s budget, so the probe must give up well before
// that instead of holding a pool connection while Postgres is unreachable.
var healthDBTimeout = 2 * time.Second

// dbHealth answers "disabled", "ok" or "down". The driver error is only
// logged: /healthz and /status are public and it can carry hostnames, user
// names and DSN fragments.
func dbHealth(ctx context.Context, store *history.Store) string {
	if store == nil {
		return "disabled"
	}
	ctx, cancel := context.WithTimeout(ctx, healthDBTimeout)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		log.Printf("health: db probe failed: %v", err)
		return "down"
	}
	return "ok"
}

// BuildInfo handles GET /status — public build metadata (commit, build time,
// version, Go version) plus a quick DB liveness flag. Public like /healthz so
// the running version can be checked with a plain curl, no token needed (the
// repo is open-source, so the commit/version aren't sensitive). The Jackett
// probe lives in the authenticated /api/status instead.
// degraded reports an edge whose database fell back to a read-only standby; the
// UI polls it to show the read-only banner.
func BuildInfo(store *history.Store, role string, degraded func() bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		info := version.Get()
		c.JSON(http.StatusOK, gin.H{
			"status":    "ok",
			"version":   info.Version,
			"commit":    info.Commit,
			"buildTime": info.BuildTime,
			"goVersion": info.GoVersion,
			"db":        dbHealth(c.Request.Context(), store),
			"role":      role,
			"degraded":  degraded(),
			"time":      time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// Health handles GET /healthz — readiness check: a bounded DB ping, no query.
// Returns 200 only when the DB is alive AND the streamer initialized; 503
// (status "degraded") otherwise, so the Docker healthcheck surfaces a process
// that came up without streaming (a silent init failure used to read healthy).
// streamerReady reports whether the streamer is up; nil means "not applicable"
// (treated as ready) so callers that don't run a streamer stay green.
func Health(store *history.Store, streamerReady func() bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		out := gin.H{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)}
		degraded := false

		dbStatus := dbHealth(c.Request.Context(), store)
		if dbStatus == "down" {
			degraded = true
		}
		out["db"] = dbStatus

		streamerStatus := "ok"
		if streamerReady != nil && !streamerReady() {
			streamerStatus = "down"
			degraded = true
		}
		out["streamer"] = streamerStatus

		code := http.StatusOK
		if degraded {
			out["status"] = "degraded"
			code = http.StatusServiceUnavailable
		}
		c.JSON(code, out)
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
				out["jackett"] = downPrefix + err.Error()
				out["status"] = "degraded"
			}
		case <-ctx.Done():
			out["jackett"] = "timeout (5s)"
			out["status"] = "degraded"
		}

		out["db"] = dbHealth(c.Request.Context(), store)
		if out["db"] == "down" {
			out["status"] = "degraded"
		}

		code := http.StatusOK
		if out["status"] == "degraded" {
			code = http.StatusServiceUnavailable
		}
		c.JSON(code, out)
	}
}
