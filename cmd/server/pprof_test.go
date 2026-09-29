package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
)

// pprofRouter builds an engine with the pprof routes and a NoRoute sentinel (599)
// that distinguishes "route not registered" from "handler ran and returned an error".
func pprofRouter(t *testing.T, deps *appDeps) (*gin.Engine, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registered := registerPprofRoutes(r, deps)
	r.NoRoute(func(c *gin.Context) { c.String(599, "NOROUTE") })
	return r, registered
}

func pprofGet(t *testing.T, r *gin.Engine, path string, header string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestPprofDisabledByDefault(t *testing.T) {
	t.Setenv("JACKUI_PPROF_ENABLED", "")
	t.Setenv("JACKUI_PPROF_TOKEN", "s3cret")

	r, registered := pprofRouter(t, &appDeps{cfg: &config.Config{}})
	if registered {
		t.Fatal("registerPprofRoutes = true without JACKUI_PPROF_ENABLED")
	}
	if code := pprofGet(t, r, "/debug/pprof/heap", ""); code != 599 {
		t.Errorf("status = %d, want 599 (route must not exist)", code)
	}
}

// With no static token and no JWT auth there is no identity to check: exposing
// a heap dump anonymously would be worse than exposing nothing.
func TestPprofNotExposedWithoutAnyAuth(t *testing.T) {
	t.Setenv("JACKUI_PPROF_ENABLED", "1")
	t.Setenv("JACKUI_PPROF_TOKEN", "")

	r, registered := pprofRouter(t, &appDeps{cfg: &config.Config{}})
	if registered {
		t.Fatal("registerPprofRoutes = true with no token and no auth")
	}
	if code := pprofGet(t, r, "/debug/pprof/heap", ""); code != 599 {
		t.Errorf("status = %d, want 599 (route must not exist)", code)
	}
}

func TestPprofStaticTokenGuard(t *testing.T) {
	t.Setenv("JACKUI_PPROF_ENABLED", "1")
	t.Setenv("JACKUI_PPROF_TOKEN", "s3cret")

	r, registered := pprofRouter(t, &appDeps{cfg: &config.Config{}})
	if !registered {
		t.Fatal("registerPprofRoutes = false with static token")
	}

	cases := []struct {
		name   string
		path   string
		header string
		want   int
	}{
		{"no token", "/debug/pprof/heap", "", http.StatusUnauthorized},
		{"wrong token", "/debug/pprof/heap?token=nope", "", http.StatusUnauthorized},
		{"wrong bearer", "/debug/pprof/heap", "Bearer nope", http.StatusUnauthorized},
		{"token in query", "/debug/pprof/heap?token=s3cret", "", http.StatusOK},
		{"correct bearer", "/debug/pprof/heap", "Bearer s3cret", http.StatusOK},
		{"index", "/debug/pprof/?token=s3cret", "", http.StatusOK},
		{"cmdline", "/debug/pprof/cmdline?token=s3cret", "", http.StatusOK},
		{"goroutine", "/debug/pprof/goroutine?token=s3cret", "", http.StatusOK},
		{"symbol", "/debug/pprof/symbol?token=s3cret", "", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := pprofGet(t, r, tc.path, tc.header); code != tc.want {
				t.Errorf("status = %d, want %d", code, tc.want)
			}
		})
	}
}

// With JWT auth on and no static token, the endpoint requires an admin JWT —
// anonymous must hit 401, not 200.
func TestPprofFallsBackToAdminJWT(t *testing.T) {
	t.Setenv("JACKUI_PPROF_ENABLED", "1")
	t.Setenv("JACKUI_PPROF_TOKEN", "")

	cfg := &config.Config{}
	cfg.Auth.Enabled = true
	deps := &appDeps{cfg: cfg, tokenMgr: auth.NewTokenManager([]byte("test-secret"), time.Minute)}

	r, registered := pprofRouter(t, deps)
	if !registered {
		t.Fatal("registerPprofRoutes = false with JWT auth enabled")
	}
	if code := pprofGet(t, r, "/debug/pprof/heap", ""); code != http.StatusUnauthorized {
		t.Errorf("anonymous status = %d, want 401", code)
	}
	// A media token (non-admin) must not open a profile either.
	if code := pprofGet(t, r, "/debug/pprof/heap?token=whatever", ""); code != http.StatusUnauthorized {
		t.Errorf("status with invalid token = %d, want 401", code)
	}
}

func TestPprofEnabledFlagParsing(t *testing.T) {
	for _, tc := range []struct {
		v    string
		want bool
	}{{"1", true}, {"true", true}, {"0", false}, {"", false}, {"yes", false}} {
		t.Setenv("JACKUI_PPROF_ENABLED", tc.v)
		if got := pprofEnabled(); got != tc.want {
			t.Errorf("pprofEnabled(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
}
