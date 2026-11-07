//go:build e2e

package e2e

import "testing"

// TestPublicRoutes covers every route registered outside the authed /api
// group (plus /api/metrics, which carries its own guard). Expected statuses
// are the reference-stack contract: ephemeral stack, empty stores, no
// external providers (Jackett URL points at 127.0.0.1:9 on purpose).
func TestPublicRoutes(t *testing.T) {
	runRoutes(t, []routeCase{
		{method: "GET", path: "/healthz", want: 200, noAuth: true},
		{method: "GET", path: "/status", want: 200, noAuth: true},
		{method: "GET", path: "/api/auth/config", want: 200, noAuth: true},
		{method: "GET", path: "/api/auth/oauth/providers", want: 200, noAuth: true},
		// Malformed JSON → the bind-error 400 branch. Probing with an empty
		// credential set instead would hit VerifyPassword and poison the
		// login lockout counter (429 on later runs) — learned the hard way.
		{method: "POST", path: "/api/auth/login", body: `{invalid`, badJSON: true, want: 400, noAuth: true},
		{method: "POST", path: "/api/auth/refresh", body: `{"refresh":"e2e-invalid-refresh-token"}`, want: 401, noAuth: true},
		{method: "POST", path: "/api/auth/verify-email", body: `{}`, want: 400, noAuth: true},
		// Anti-enumeration contract: unknown email still answers 200.
		{method: "POST", path: "/api/auth/forgot", body: `{"email":"e2e-missing@invalid"}`, want: 200, noAuth: true},
		{method: "POST", path: "/api/auth/reset", body: `{}`, want: 400, noAuth: true},
		// Passkeys are opt-in (JACKUI_PASSKEY_*/webauthn RP unset) → 503.
		{method: "POST", path: "/api/auth/passkey/login/begin", body: `{}`, want: 503, noAuth: true},
		{method: "POST", path: "/api/auth/passkey/login/finish", body: `{}`, want: 503, noAuth: true},
		// Prometheus metrics: admin JWT reaches promhttp; no static scraper
		// token is configured on the reference stack.
		{method: "GET", path: "/api/metrics", want: 200},
		// Control-plane route authenticated by JACKUI_CONTROL_TOKEN; unset on
		// the reference stack → the handler answers 503 before any auth check.
		{method: "POST", path: "/api/stream/peer-port/refresh", want: 503, noAuth: true},
	})
}
