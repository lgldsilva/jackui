package main

import (
	"crypto/subtle"
	"log"
	"net/http/pprof"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
)

// pprofEnabled tells whether /debug/pprof should be exposed. Opt-in (default
// OFF): the profiles carry heap/goroutines, i.e. torrent names, file paths and
// in-flight tokens.
func pprofEnabled() bool {
	v := os.Getenv("JACKUI_PPROF_ENABLED")
	return v == "1" || v == "true"
}

// staticTokenGuard aborts with 401 when the static token does not match. Accepts
// `Authorization: Bearer <token>` or `?token=` — `go tool pprof <url>` has no way
// to send a header, so the query is the realistic path. Constant-time compare.
func staticTokenGuard(static string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if subtle.ConstantTimeCompare([]byte(presentedToken(c)), []byte(static)) != 1 {
			c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

// presentedToken extracts the token from the Bearer header or, failing that, from the query.
func presentedToken(c *gin.Context) string {
	authz := c.GetHeader("Authorization")
	presented := strings.TrimPrefix(authz, "Bearer ")
	if presented == "" || presented == authz {
		presented = c.Query("token")
	}
	return presented
}

// registerPprofRoutes exposes net/http/pprof under /debug/pprof, always authenticated.
//
// Two modes, neither anonymous:
//   - JACKUI_PPROF_TOKEN set → static token (header or ?token=), the only way
//     for `go tool pprof` to reach the endpoint without a browser.
//   - no token, but JWT auth enabled → requires an admin JWT.
//
// With no token AND no auth there is no identity to check at all, so the routes
// are NOT registered (explicit log) instead of opening a memory dump to the
// whole network. Returns true when registered.
func registerPprofRoutes(router gin.IRouter, deps *appDeps) bool {
	if !pprofEnabled() {
		return false
	}

	static := strings.TrimSpace(os.Getenv("JACKUI_PPROF_TOKEN"))
	jwtAvailable := deps != nil && deps.cfg != nil && deps.cfg.Auth.Enabled && deps.tokenMgr != nil

	var guards []gin.HandlerFunc
	switch {
	case static != "":
		guards = append(guards, staticTokenGuard(static))
	case jwtAvailable:
		guards = append(guards, auth.Required(deps.tokenMgr), auth.AdminOnly())
	default:
		log.Printf("pprof: JACKUI_PPROF_ENABLED is on but there is no way to authenticate " +
			"(set JACKUI_PPROF_TOKEN or enable JWT auth) — /debug/pprof was NOT exposed")
		return false
	}

	group := router.Group("/debug/pprof", guards...)
	group.GET("/", gin.WrapF(pprof.Index))
	group.GET("/cmdline", gin.WrapF(pprof.Cmdline))
	group.GET("/profile", gin.WrapF(pprof.Profile))
	group.GET("/trace", gin.WrapF(pprof.Trace))
	group.GET("/symbol", gin.WrapF(pprof.Symbol))
	group.POST("/symbol", gin.WrapF(pprof.Symbol))
	// Named profiles, explicit: a :profile wildcard would collide with the
	// static routes above at the same position in gin's route tree.
	for _, name := range []string{"heap", "goroutine", "allocs", "block", "mutex", "threadcreate"} {
		group.GET("/"+name, gin.WrapH(pprof.Handler(name)))
	}
	return true
}
