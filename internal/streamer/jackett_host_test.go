package streamer

import (
	"sync"
	"testing"
)

func TestUpdateJackett(t *testing.T) {
	s := NewForTesting()
	s.UpdateJackett("http://jackett.local:9117/api", "k1")
	if addr, key := s.jackett(); addr != "jackett.local:9117" || key != "k1" {
		t.Errorf("jackett() = %q,%q, want jackett.local:9117,k1", addr, key)
	}
	// Re-point to a new host (the live-config-update path); default port.
	s.UpdateJackett("https://10.0.0.5", "k2")
	if addr, key := s.jackett(); addr != "10.0.0.5:443" || key != "k2" {
		t.Errorf("jackett() = %q,%q, want 10.0.0.5:443,k2", addr, key)
	}
	// Unparseable URL leaves the previous address untouched.
	s.UpdateJackett("://broken", "k3")
	if addr, _ := s.jackett(); addr != "10.0.0.5:443" {
		t.Errorf("addr after bad url = %q, want unchanged 10.0.0.5:443", addr)
	}
}

func TestJackettAddrFromURL(t *testing.T) {
	cases := map[string]string{
		"http://jackett:9117":       "jackett:9117",
		"http://Jackett.LAN/":       "jackett.lan:80",
		"https://jackett.lan/api":   "jackett.lan:443",
		"http://[fd00::1]:9117/api": "[fd00::1]:9117",
	}
	for in, want := range cases {
		if got, ok := JackettAddrFromURL(in); !ok || got != want {
			t.Errorf("JackettAddrFromURL(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "://x", "ftp://jackett", "jackett:9117"} {
		if got, ok := JackettAddrFromURL(bad); ok {
			t.Errorf("JackettAddrFromURL(%q) = %q, want !ok", bad, got)
		}
	}
}

// TestJackettConfigConcurrentUpdate is a -race guard: PUT /config rewrites
// the Jackett address/key while torrent fetches read them.
func TestJackettConfigConcurrentUpdate(t *testing.T) {
	s := NewForTesting()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); s.UpdateJackett("http://jackett:9117", "k") }()
		go func() { defer wg.Done(); _ = s.injectJackettAPIKey("http://jackett:9117/dl") }()
	}
	wg.Wait()
}
