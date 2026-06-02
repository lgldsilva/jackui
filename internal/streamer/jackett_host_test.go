package streamer

import "testing"

func TestUpdateJackettHost(t *testing.T) {
	s := NewForTesting()
	s.UpdateJackettHost("http://jackett.local:9117/api")
	if got := s.cfg.JackettHost; got != "jackett.local" {
		t.Errorf("JackettHost = %q, want %q", got, "jackett.local")
	}
	// Re-point to a new host (the live-config-update path).
	s.UpdateJackettHost("https://10.0.0.5:9117")
	if got := s.cfg.JackettHost; got != "10.0.0.5" {
		t.Errorf("JackettHost = %q, want %q", got, "10.0.0.5")
	}
	// Unparseable URL leaves the previous host untouched.
	s.UpdateJackettHost("://broken")
	if got := s.cfg.JackettHost; got != "10.0.0.5" {
		t.Errorf("JackettHost after bad url = %q, want unchanged %q", got, "10.0.0.5")
	}
}
