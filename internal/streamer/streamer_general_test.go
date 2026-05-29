package streamer

import (
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCleanSource(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"already clean", "magnet:?xt=urn:btih:abc", "magnet:?xt=urn:btih:abc"},
		{"with BOM", "\xef\xbb\xbfmagnet:?xt=urn:btih:abc", "magnet:?xt=urn:btih:abc"},
		{"spaces", "  magnet:?xt=urn:btih:abc  ", "magnet:?xt=urn:btih:abc"},
		{"BOM+spaces", " \xef\xbb\xbfmagnet:?xt=urn:btih:abc ", "magnet:?xt=urn:btih:abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanSource(tt.src); got != tt.want {
				t.Errorf("cleanSource() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsMagnet(t *testing.T) {
	tests := []struct {
		name  string
		lower string
		src   string
		want  bool
	}{
		{"magnet prefix", "magnet:?xt=urn:", "magnet:?xt=urn:btih:abc", true},
		{"contains magnet", "http://example.com/magnet:", "http://example.com/magnet:?xt=urn:btih:abc", true},
		{"not magnet", "http://example.", "http://example.com/file.torrent", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMagnet(tt.lower, tt.src); got != tt.want {
				t.Errorf("isMagnet() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFmtBytes(t *testing.T) {
	tests := []struct {
		name string
		n    int64
		want string
	}{
		{"bytes", 500, "500 B"},
		{"KB", 2048, "2.00 KB"},
		{"MB", 1048576, "1.00 MB"},
		{"GB", 1073741824, "1.00 GB"},
		{"zero", 0, "0 B"},
		{"TB", 1099511627776, "1.00 TB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fmtBytes(tt.n); got != tt.want {
				t.Errorf("fmtBytes() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewForTesting(t *testing.T) {
	s := NewForTesting()
	if s == nil {
		t.Fatal("NewForTesting() returned nil")
	}
	if s.active == nil {
		t.Error("active map is nil")
	}
	if s.downloads == nil {
		t.Error("downloads map is nil")
	}
	if s.dlLimiter == nil {
		t.Error("dlLimiter is nil")
	}
	if s.upLimiter == nil {
		t.Error("upLimiter is nil")
	}
}

func TestFavoritesNilSafe(t *testing.T) {
	var s *Streamer
	if f := s.Favorites(); f != nil {
		t.Error("Favorites() on nil streamer should return nil")
	}
}

func TestRateLimits(t *testing.T) {
	s := NewForTesting()
	d, u := s.RateLimits()
	if d != 0 || u != 0 {
		t.Errorf("initial limits: dl=%d, ul=%d; want 0, 0", d, u)
	}

	s.SetRateLimits(1000, 500)
	d, u = s.RateLimits()
	if d != 1000 || u != 500 {
		t.Errorf("after set: dl=%d, ul=%d; want 1000, 500", d, u)
	}
}

func TestGlobalStats_Empty(t *testing.T) {
	s := NewForTesting()
	g := s.GlobalStats()
	if g.ActiveTorrents != 0 {
		t.Errorf("ActiveTorrents = %d, want 0", g.ActiveTorrents)
	}
}

func TestParseMagnet_Invalid(t *testing.T) {
	s := NewForTesting()
	_, _, err := s.ParseMagnet("not-a-magnet")
	if err == nil {
		t.Error("expected error for invalid magnet")
	}
}

func TestIsBlockedFetchIP(t *testing.T) {
	tests := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.1.1", true},
		{"0.0.0.0", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"203.0.113.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("invalid IP: %s", tt.ip)
			}
			if got := isBlockedFetchIP(ip); got != tt.blocked {
				t.Errorf("isBlockedFetchIP(%s) = %v, want %v", tt.ip, got, tt.blocked)
			}
		})
	}
}

func TestInjectJackettAPIKey(t *testing.T) {
	tests := []struct {
		name        string
		jackettHost string
		apiKey      string
		torrentURL  string
		want        string
	}{
		{"no host", "", "", "http://example.com/dl/1", "http://example.com/dl/1"},
		{"different host", "jackett.local", "key123", "http://other.com/dl/1", "http://other.com/dl/1"},
		{"injects key", "jackett.local", "key123", "http://jackett.local/dl/1", "http://jackett.local/dl/1?apikey=key123"},
		{"already has key", "jackett.local", "key123", "http://jackett.local/dl/1?apikey=existing", "http://jackett.local/dl/1?apikey=existing"},
		{"no api key", "jackett.local", "", "http://jackett.local/dl/1", "http://jackett.local/dl/1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewForTesting()
			s.cfg.JackettHost = tt.jackettHost
			s.cfg.JackettAPIKey = tt.apiKey
			if got := s.injectJackettAPIKey(tt.torrentURL); got != tt.want {
				t.Errorf("injectJackettAPIKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckRedirect(t *testing.T) {
	magnet := ""
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	err := checkRedirect(req, nil, &magnet)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	magnetReq, _ := http.NewRequest("GET", "magnet:?xt=urn:btih:abc", nil)
	err = checkRedirect(magnetReq, nil, &magnet)
	if err != http.ErrUseLastResponse {
		t.Errorf("expected ErrUseLastResponse, got %v", err)
	}
	if magnet != "magnet:?xt=urn:btih:abc" {
		t.Errorf("magnet = %q", magnet)
	}
	tooMany := make([]*http.Request, 10)
	err = checkRedirect(req, tooMany, &magnet)
	if err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Errorf("expected too many redirects error, got %v", err)
	}
}

type fileInfoMock struct {
	size int64
}

func (f fileInfoMock) Name() string       { return "mock" }
func (f fileInfoMock) Size() int64        { return f.size }
func (f fileInfoMock) Mode() os.FileMode  { return 0 }
func (f fileInfoMock) ModTime() time.Time { return time.Time{} }
func (f fileInfoMock) IsDir() bool        { return false }
func (f fileInfoMock) Sys() interface{}   { return nil }

func TestPhysicalBytes(t *testing.T) {
	info := fileInfoMock{size: 100}
	if pb := PhysicalBytes(info); pb != 100 {
		t.Errorf("PhysicalBytes(fallback) = %d, want 100", pb)
	}
}
