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

// pprofRouter monta um engine com as rotas de pprof e um NoRoute sentinela (599)
// que distingue "rota não registrada" de "handler rodou e devolveu erro".
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
		t.Fatal("registerPprofRoutes = true sem JACKUI_PPROF_ENABLED")
	}
	if code := pprofGet(t, r, "/debug/pprof/heap", ""); code != 599 {
		t.Errorf("status = %d, want 599 (rota não deve existir)", code)
	}
}

// Sem token estático e sem auth JWT não há identidade para checar: expor um
// dump de heap anonimamente seria pior que não expor nada.
func TestPprofNotExposedWithoutAnyAuth(t *testing.T) {
	t.Setenv("JACKUI_PPROF_ENABLED", "1")
	t.Setenv("JACKUI_PPROF_TOKEN", "")

	r, registered := pprofRouter(t, &appDeps{cfg: &config.Config{}})
	if registered {
		t.Fatal("registerPprofRoutes = true sem token e sem auth")
	}
	if code := pprofGet(t, r, "/debug/pprof/heap", ""); code != 599 {
		t.Errorf("status = %d, want 599 (rota não deve existir)", code)
	}
}

func TestPprofStaticTokenGuard(t *testing.T) {
	t.Setenv("JACKUI_PPROF_ENABLED", "1")
	t.Setenv("JACKUI_PPROF_TOKEN", "s3cret")

	r, registered := pprofRouter(t, &appDeps{cfg: &config.Config{}})
	if !registered {
		t.Fatal("registerPprofRoutes = false com token estático")
	}

	cases := []struct {
		name   string
		path   string
		header string
		want   int
	}{
		{"sem token", "/debug/pprof/heap", "", http.StatusUnauthorized},
		{"token errado", "/debug/pprof/heap?token=nope", "", http.StatusUnauthorized},
		{"bearer errado", "/debug/pprof/heap", "Bearer nope", http.StatusUnauthorized},
		{"token na query", "/debug/pprof/heap?token=s3cret", "", http.StatusOK},
		{"bearer certo", "/debug/pprof/heap", "Bearer s3cret", http.StatusOK},
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

// Com auth JWT ligada e sem token estático, o endpoint exige JWT de admin —
// anônimo tem de bater em 401, não em 200.
func TestPprofFallsBackToAdminJWT(t *testing.T) {
	t.Setenv("JACKUI_PPROF_ENABLED", "1")
	t.Setenv("JACKUI_PPROF_TOKEN", "")

	cfg := &config.Config{}
	cfg.Auth.Enabled = true
	deps := &appDeps{cfg: cfg, tokenMgr: auth.NewTokenManager([]byte("test-secret"), time.Minute)}

	r, registered := pprofRouter(t, deps)
	if !registered {
		t.Fatal("registerPprofRoutes = false com auth JWT habilitada")
	}
	if code := pprofGet(t, r, "/debug/pprof/heap", ""); code != http.StatusUnauthorized {
		t.Errorf("status anônimo = %d, want 401", code)
	}
	// Um media token (não-admin) também não pode abrir profile.
	if code := pprofGet(t, r, "/debug/pprof/heap?token=whatever", ""); code != http.StatusUnauthorized {
		t.Errorf("status com token inválido = %d, want 401", code)
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
