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
