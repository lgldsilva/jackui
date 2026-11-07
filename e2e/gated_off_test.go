//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// The route inventory contains feature-gated surfaces that are OFF on the
// reference stack (no JACKUI_PPROF_ENABLED, JACKUI_TRANSMISSION_RPC_ENABLED
// or JACKUI_OAUTH_* env). These tests pin that OFF state explicitly: the
// gated handlers must NOT answer, and the request must fall through to the
// generic catch-alls (SPA fallback for non-/api paths, 404 JSON for /api/*).
// When an operator enables the feature, these tests fail — the signal to
// move those routes into the covered tables.
func TestGatedOffRoutes(t *testing.T) {
	t.Run("oauth start not registered", func(t *testing.T) {
		status, body, err := app.rawDo("GET", "/api/auth/oauth/google/start", app.token, "")
		if err != nil {
			t.Fatalf("%v", err)
		}
		if status != http.StatusNotFound || !strings.Contains(body, "endpoint not found") {
			t.Fatalf("oauth start should be unregistered (404 endpoint not found), got %d %s", status, snippet(body))
		}
	})
	t.Run("oauth callback not registered", func(t *testing.T) {
		status, body, err := app.rawDo("GET", "/api/auth/oauth/google/callback", "", "")
		if err != nil {
			t.Fatalf("%v", err)
		}
		if status != http.StatusNotFound || !strings.Contains(body, "endpoint not found") {
			t.Fatalf("oauth callback should be unregistered, got %d %s", status, snippet(body))
		}
	})
	t.Run("oauth exchange not registered", func(t *testing.T) {
		status, body, err := app.rawDo("POST", "/api/auth/oauth/exchange", "", `{}`)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if status != http.StatusNotFound || !strings.Contains(body, "endpoint not found") {
			t.Fatalf("oauth exchange should be unregistered, got %d %s", status, snippet(body))
		}
	})
	t.Run("transmission rpc not registered", func(t *testing.T) {
		status, body, err := app.rawDo("GET", "/transmission/rpc", "", "")
		if err != nil {
			t.Fatalf("%v", err)
		}
		// The real handler answers 409 with X-Transmission-Session-Id; the SPA
		// fallback answers 200 HTML without it.
		if status != http.StatusOK || !strings.Contains(body, "<div id=\"root\">") {
			t.Fatalf("expected SPA fallback HTML, got %d %s", status, snippet(body))
		}
	})
	t.Run("pprof not registered", func(t *testing.T) {
		status, body, err := app.rawDo("GET", "/debug/pprof/cmdline", app.token, "")
		if err != nil {
			t.Fatalf("%v", err)
		}
		// pprof.Cmdline would answer text/plain with the process args; the SPA
		// fallback answers the app shell.
		if status != http.StatusOK || !strings.Contains(body, "<div id=\"root\">") {
			t.Fatalf("expected SPA fallback HTML, got %d %s", status, snippet(body))
		}
	})
}
