//go:build e2e

package e2e

import "testing"

// TestCoreRoutes covers the non-streaming API surface: search/jackett,
// config/admin, AI benchmark, history, library, stats, transfers, push,
// subtitles, TMDB and the authenticated auth routes.
func TestCoreRoutes(t *testing.T) {
	runRoutes(t, []routeCase{
		// --- search / jackett / proxy ---
		{method: "GET", path: "/api/classify", want: 400},
		{method: "POST", path: "/api/diag/log", body: `{}`, want: 400},
		{method: "GET", path: "/api/search?q=e2e", want: 502},
		// SSE: the search stream opens (200) and emits progress/error events,
		// closing once the dead Jackett connection fails.
		{method: "GET", path: "/api/search/stream?q=e2e", want: 200},
		{method: "GET", path: "/api/indexers", want: 502},
		{method: "POST", path: "/api/download", body: `{}`, want: 400},
		{method: "GET", path: "/api/clients", want: 200},
		{method: "GET", path: "/api/proxy/torrent", want: 400},
		{method: "GET", path: "/api/convert/torrent-to-magnet", want: 400},
		{method: "GET", path: "/api/convert/magnet-to-torrent", want: 400},

		// --- admin config / settings ---
		{method: "GET", path: "/api/config", want: 200},
		// Valid body: the handler SAVES whatever it binds, so the probe must
		// keep the reference stack's values sane (port + dead jackett URL).
		{method: "PUT", path: "/api/config", body: `{"port":8989,"jackett":{"url":"http://127.0.0.1:9"}}`, want: 200},
		{method: "GET", path: "/api/stream/settings", want: 200},
		{method: "PUT", path: "/api/stream/settings", body: `{}`, want: 400},
		{method: "PUT", path: "/api/downloads/settings", body: `{}`, want: 400},
		{method: "POST", path: "/api/settings/subtitles", body: `{}`, want: 400},
		{method: "GET", path: "/api/mounts", want: 200},
		{method: "PUT", path: "/api/mounts", body: `{}`, want: 400},
		{method: "POST", path: "/api/config/test", body: `{"url":"http://127.0.0.1:9","apiKey":"e2e"}`, want: 502},

		// --- AI benchmark (no AI provider configured → chain disabled) ---
		{method: "GET", path: "/api/ai/benchmark", want: 200},
		{method: "POST", path: "/api/ai/benchmark", body: `{}`, want: 503},
		// Nothing running to cancel → handler answers 404, not 200.
		{method: "POST", path: "/api/ai/benchmark/cancel", want: 404},
		{method: "POST", path: "/api/ai/benchmark/rerun-incomplete", body: `{}`, want: 503},
		{method: "PUT", path: "/api/ai/benchmark/cases", body: `{}`, want: 503},
		{method: "PUT", path: "/api/ai/settings", body: `{}`, want: 503},

		// --- admin cross-user downloads ---
		{method: "GET", path: "/api/downloads/all", want: 200},
		{method: "GET", path: "/api/downloads/users", want: 200},

		// --- status / history ---
		{method: "GET", path: "/api/status", want: 503},
		{method: "GET", path: "/api/history?page=1", want: 200},
		{method: "GET", path: "/api/history/results?q=e2e", want: 200},
		{method: "GET", path: "/api/history/cache?q=e2e", want: 200},
		{method: "DELETE", path: "/api/history", want: 200},
		{method: "POST", path: "/api/history/" + bogusID + "/refresh", want: 404},

		// --- library / recommendations / stats ---
		{method: "GET", path: "/api/library", want: 200},
		{method: "GET", path: "/api/library/hash/" + bogusHash, want: 404},
		{method: "GET", path: "/api/recommendations", want: 200},
		{method: "POST", path: "/api/recommendations/dismiss", body: `{}`, want: 400},
		{method: "GET", path: "/api/stats", want: 200},
		{method: "GET", path: "/api/library/" + bogusID, want: 404},
		// The resume-position ping is a no-op on a missing entry (player keeps
		// pinging after a concurrent delete) — that's the pinned contract.
		{method: "PATCH", path: "/api/library/" + bogusID, body: `{}`, want: 200},
		// Store not-found maps to 404 (sentinel fix, sweep 2026-11-06 §3.2.6).
		{method: "DELETE", path: "/api/library/" + bogusID, want: 404},
		{method: "DELETE", path: "/api/library", want: 200},

		// --- transfers ---
		{method: "GET", path: "/api/transfers", want: 200},
		{method: "DELETE", path: "/api/transfers/" + bogusID, want: 404},

		// --- push / notifications ---
		{method: "GET", path: "/api/push/vapid", want: 200},
		{method: "POST", path: "/api/push/subscribe", body: `{}`, want: 400},
		{method: "POST", path: "/api/push/unsubscribe", body: `{}`, want: 400},
		{method: "GET", path: "/api/notifications", want: 200},
		{method: "POST", path: "/api/notifications/read", body: `{"ids":[]}`, want: 200},

		// --- subtitles (OpenSubtitles disabled on the reference stack) ---
		{method: "GET", path: "/api/settings/subtitles", want: 200},
		{method: "GET", path: "/api/subtitles/search?q=e2e", want: 502},
		{method: "GET", path: "/api/subtitles/download/e2e-missing", want: 502},
		{method: "GET", path: "/api/subtitles/enabled", want: 200},
		{method: "GET", path: "/api/subtitles/auto/" + bogusHash + "/" + bogusFile, want: 404},
		{method: "GET", path: "/api/stream/" + bogusHash + "/" + bogusFile + "/subtitles/auto", want: 404},

		// --- TMDB (no API key on the reference stack) ---
		{method: "GET", path: "/api/tmdb/match", want: 400},
		{method: "POST", path: "/api/tmdb/match/batch", body: `{}`, want: 400},
		{method: "GET", path: "/api/tmdb/trending", want: 503},
		{method: "GET", path: "/api/tmdb/genres", want: 503},
		{method: "GET", path: "/api/tmdb/videos", want: 400},

		// --- misc ---
		{method: "GET", path: "/api/transcode/capabilities", want: 200},
		{method: "GET", path: "/api/lyrics", want: 400},
		// The trending provider (Apple Music RSS) is enabled by default and the
		// reference stack has egress — the route answers live data.
		{method: "GET", path: "/api/music/trending", want: 200},

		// --- authenticated auth routes ---
		{method: "GET", path: "/api/auth/me", want: 200},
		{method: "POST", path: "/api/auth/password", body: `{}`, want: 400},
		{method: "POST", path: "/api/auth/email", body: `{}`, want: 400},
		// Stages a TOTP secret for the caller; MFA stays disabled until
		// /api/auth/mfa/verify confirms, so login keeps working.
		{method: "POST", path: "/api/auth/mfa/enroll", body: `{}`, want: 200},
		{method: "POST", path: "/api/auth/mfa/verify", body: `{}`, want: 400},
		{method: "POST", path: "/api/auth/mfa/disable", body: `{}`, want: 400},
		{method: "GET", path: "/api/auth/mfa/backup-codes", want: 200},
		{method: "POST", path: "/api/auth/mfa/backup-codes/regenerate", body: `{}`, want: 400},
		{method: "POST", path: "/api/auth/media-token", body: `{}`, want: 200},
		{method: "POST", path: "/api/auth/sessions", want: 200},
		// The refresh token is what identifies the session family to revoke.
		{method: "POST", path: "/api/auth/sessions/revoke-others", body: `{}`, want: 400},
		// Idempotent: unknown session id still answers 200 "session terminated".
		{method: "DELETE", path: "/api/auth/sessions/" + bogusID, want: 200},
		{method: "GET", path: "/api/auth/passkey", want: 200},
		{method: "POST", path: "/api/auth/passkey/register/begin", body: `{}`, want: 503},
		{method: "POST", path: "/api/auth/passkey/register/finish", body: `{}`, want: 503},
		// Idempotent delete of an unknown passkey → 200.
		{method: "DELETE", path: "/api/auth/passkey/" + bogusID, want: 200},
		{method: "POST", path: "/api/user/ntfy-topic", body: `{"topic":""}`, want: 200},
		// Without an ntfy topic configured the test notification can't go out.
		{method: "POST", path: "/api/user/notify-test", body: `{}`, want: 400},
		{method: "DELETE", path: "/api/user/incognito", want: 200},
		{method: "POST", path: "/api/user/incognito/heartbeat", body: `{}`, want: 204},

		// --- admin user management ---
		{method: "GET", path: "/api/auth/users", want: 200},
		{method: "POST", path: "/api/auth/users", body: `{}`, want: 400},
		// Idempotent delete of an unknown user → 200 "deleted".
		{method: "DELETE", path: "/api/auth/users/" + bogusID, want: 200},
		// Existing user required before the status value is validated → 400.
		{method: "PATCH", path: "/api/auth/users/" + bogusID + "/status", body: `{}`, want: 400},
		// Invites need a public base URL, unset on the reference stack → 500.
		{method: "POST", path: "/api/auth/users/invite", body: `{}`, want: 500},
		{method: "POST", path: "/api/auth/users/" + bogusID + "/reset-password", body: `{}`, want: 404},
		{method: "GET", path: "/api/auth/users/" + bogusID + "/sessions", want: 404},
		{method: "DELETE", path: "/api/auth/users/" + bogusID + "/sessions", want: 404},
		{method: "DELETE", path: "/api/auth/users/" + bogusID + "/sessions/" + bogusID, want: 404},
	})
}
