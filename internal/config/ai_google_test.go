package config

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsFreeGoogleModel(t *testing.T) {
	for _, m := range []string{"gemini-2.5-flash-lite", "gemini-2.5-flash", "gemini-2.0-flash"} {
		if !IsFreeGoogleModel(m) {
			t.Errorf("expected %q to be free", m)
		}
	}
	// Paid / unknown ids must NEVER be treated as free — this is the guard that keeps
	// the benchmark from spending on a paid look-alike (e.g. gemini-3.5-flash).
	for _, m := range []string{"gemini-3.5-flash", "gemini-2.5-pro", "gemini-1.5-pro", "gemini-2.5-flash-lite-preview", "flash", ""} {
		if IsFreeGoogleModel(m) {
			t.Errorf("expected %q NOT to be free", m)
		}
	}
}

func TestPickFreeGoogleModel(t *testing.T) {
	// Prefers the highest-preference free id that is actually served.
	if got := pickFreeGoogleModel([]string{"gemini-3.5-flash", "gemini-2.5-flash", "gemini-2.5-flash-lite"}); got != "gemini-2.5-flash-lite" {
		t.Errorf("want gemini-2.5-flash-lite, got %q", got)
	}
	// Falls through to the next preference when the top isn't served.
	if got := pickFreeGoogleModel([]string{"gemini-2.5-flash", "gemini-2.5-pro"}); got != "gemini-2.5-flash" {
		t.Errorf("want gemini-2.5-flash, got %q", got)
	}
	// Never returns a paid model; empty when nothing free is served or list is nil.
	if got := pickFreeGoogleModel([]string{"gemini-3.5-flash", "gemini-2.5-pro"}); got != "" {
		t.Errorf("want empty (no free served), got %q", got)
	}
	if got := pickFreeGoogleModel(nil); got != "" {
		t.Errorf("want empty for nil, got %q", got)
	}
}

func TestApplyAIEnv_GeminiKey(t *testing.T) {
	cfg := &Config{}
	t.Setenv("GEMINI_API_KEY", "gemkey123")
	applyAIEnv(cfg)
	p := cfg.AI.Providers["google"]
	if p.APIKey != "gemkey123" {
		t.Fatalf("google APIKey = %q", p.APIKey)
	}
	if p.BaseURL != "https://generativelanguage.googleapis.com/v1beta/openai/" {
		t.Fatalf("google BaseURL = %q", p.BaseURL)
	}
}

func TestAppendGoogleSlot_NoKey(t *testing.T) {
	// A provider entry with no key yields no slot (graceful skip).
	cfg := &Config{AI: AIConfig{Providers: map[string]AIProvider{"google": {BaseURL: "https://x", APIKey: ""}}}}
	if chain := cfg.appendGoogleSlot(nil); len(chain) != 0 {
		t.Fatalf("expected no slot without a key, got %d", len(chain))
	}
	// No google provider configured at all → no slot, no panic.
	if chain := (&Config{}).appendGoogleSlot(nil); len(chain) != 0 {
		t.Fatalf("expected no slot without provider, got %d", len(chain))
	}
}

// TestAppendGoogleSlot_PicksFreeFromDiscovery drives the happy path against a fake
// /models endpoint: given a catalog mixing free and paid ids, the slot must be the
// preferred FREE model — never the paid gemini-3.5-flash / pro that come back too.
func TestAppendGoogleSlot_PicksFreeFromDiscovery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gemini-3.5-flash"},{"id":"gemini-2.5-pro"},{"id":"gemini-2.5-flash"},{"id":"gemini-2.5-flash-lite"}]}`))
	}))
	defer srv.Close()

	cfg := &Config{AI: AIConfig{Providers: map[string]AIProvider{"google": {BaseURL: srv.URL, APIKey: "k"}}}}
	chain := cfg.appendGoogleSlot(nil)
	if len(chain) != 1 {
		t.Fatalf("expected exactly one google slot, got %d", len(chain))
	}
	if chain[0].Model != "gemini-2.5-flash-lite" {
		t.Errorf("expected the free flash-lite, got %q (must never pick paid 3.5-flash/pro)", chain[0].Model)
	}
	if chain[0].Provider != "google" {
		t.Errorf("provider = %q, want google", chain[0].Provider)
	}
}

// TestAppendGoogleSlot_NoFreeServed: discovery returns only paid models → no slot
// (we never seed a paid model into the chain).
func TestAppendGoogleSlot_NoFreeServed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"gemini-3.5-flash"},{"id":"gemini-2.5-pro"}]}`))
	}))
	defer srv.Close()
	cfg := &Config{AI: AIConfig{Providers: map[string]AIProvider{"google": {BaseURL: srv.URL, APIKey: "k"}}}}
	if chain := cfg.appendGoogleSlot(nil); len(chain) != 0 {
		t.Fatalf("expected no slot when only paid models are served, got %d", len(chain))
	}
}
