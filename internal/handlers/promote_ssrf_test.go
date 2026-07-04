package handlers

import (
	"net"
	"testing"
)

// TestSSRFGuards locks the /convert/torrent-to-magnet fetch guards: only
// http/https schemes, loopback + link-local/cloud-metadata blocked, private LAN
// (Jackett at 192.168.x) and public IPs allowed.
func TestSSRFGuards(t *testing.T) {
	if err := validateFetchScheme("file:///etc/passwd"); err == nil {
		t.Error("file:// scheme should be rejected")
	}
	if err := validateFetchScheme("gopher://x/"); err == nil {
		t.Error("gopher:// scheme should be rejected")
	}
	if err := validateFetchScheme("http://192.168.1.50/x.torrent"); err != nil {
		t.Errorf("http should be allowed: %v", err)
	}
	if err := validateFetchScheme("https://tracker.example/x.torrent"); err != nil {
		t.Errorf("https should be allowed: %v", err)
	}

	blocked := []string{"127.0.0.1", "::1", "169.254.169.254", "0.0.0.0"}
	for _, s := range blocked {
		if !isBlockedFetchIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	allowed := []string{"192.168.1.50", "10.0.0.5", "172.16.0.9", "8.8.8.8"}
	for _, s := range allowed {
		if isBlockedFetchIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed (Jackett LAN / public)", s)
		}
	}
}
