package config

import "testing"

func TestActiveEnvOverrides_MasksSecretsAndReportsSet(t *testing.T) {
	// Non-secret values are echoed verbatim; secrets are masked.
	t.Setenv("JACKUI_BASE_URL", "https://jackui.example")
	t.Setenv("JACKETT_API_KEY", "super-secret-key")
	t.Setenv("GROQ_API_KEY", "groq-secret")

	out := ActiveEnvOverrides()

	if got := out["JACKUI_BASE_URL"]; got != "https://jackui.example" {
		t.Errorf("JACKUI_BASE_URL = %q, want verbatim", got)
	}
	if got := out["JACKETT_API_KEY"]; got != "••••••" {
		t.Errorf("JACKETT_API_KEY = %q, want masked", got)
	}
	if got := out["GROQ_API_KEY"]; got != "••••••" {
		t.Errorf("GROQ_API_KEY = %q, want masked", got)
	}
}

func TestActiveEnvOverrides_OmitsUnsetKeys(t *testing.T) {
	// An env var that is empty-but-set still reports; an unset one is absent.
	t.Setenv("JACKUI_NTFY_TOPIC", "")
	out := ActiveEnvOverrides()
	if _, ok := out["JACKUI_NTFY_TOPIC"]; !ok {
		t.Error("set-but-empty key should be present")
	}
	// A key we never set in this test process should be absent (best-effort:
	// pick one unlikely to be in the env).
	if _, ok := out["JACKUI_SMTP_FROM"]; ok {
		t.Skip("JACKUI_SMTP_FROM unexpectedly set in env; skipping absence check")
	}
}

// Regression: TMDB_API_KEY and OMDB_API_KEY are real API credentials that were
// missing from the mask list, so ActiveEnvOverrides echoed them verbatim into
// the admin GET /api/config response and they showed up in plaintext on the
// Settings screen. Every key that holds a secret must be masked, not just the
// auth/AI ones.
func TestActiveEnvOverrides_MasksMediaDBTokens(t *testing.T) {
	t.Setenv("TMDB_API_KEY", "tmdb-secret-token")
	t.Setenv("OMDB_API_KEY", "omdb-secret-token")
	t.Setenv("JACKUI_NTFY_TOKEN", "tk_ntfy-secret")

	out := ActiveEnvOverrides()

	for _, k := range []string{"TMDB_API_KEY", "OMDB_API_KEY", "JACKUI_NTFY_TOKEN"} {
		if got := out[k]; got != "••••••" {
			t.Errorf("%s = %q, want masked ••••••", k, got)
		}
	}
}

func TestApplyNotificationsEnv_NtfyToken(t *testing.T) {
	t.Setenv("JACKUI_NTFY_TOKEN", "tk_secret")
	cfg := &Config{}
	applyNotificationsEnv(cfg)
	if cfg.Notifications.NtfyToken != "tk_secret" {
		t.Errorf("NtfyToken = %q, want tk_secret", cfg.Notifications.NtfyToken)
	}
}
