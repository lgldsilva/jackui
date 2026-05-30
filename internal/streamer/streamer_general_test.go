package streamer

import (
	"strings"
	"testing"
)

func TestNewForTesting(t *testing.T) {
	s := NewForTesting()
	if s == nil {
		t.Fatal("expected non-nil Streamer")
	}
	if s.Favorites() != nil {
		t.Fatal("new testing streamer should have nil favorites")
	}
}

func TestGlobalStats_Empty(t *testing.T) {
	s := NewForTesting()
	stats := s.GlobalStats()
	if stats.ActiveTorrents != 0 {
		t.Errorf("expected 0 active torrents, got %d", stats.ActiveTorrents)
	}
	if stats.DownRate != 0 || stats.UpRate != 0 {
		t.Errorf("expected 0 rates, got down=%d up=%d", stats.DownRate, stats.UpRate)
	}
}

func TestRateLimits_Default(t *testing.T) {
	s := NewForTesting()
	down, up := s.RateLimits()
	if down != 0 {
		t.Errorf("expected unlimited down (0), got %d", down)
	}
	if up != 0 {
		t.Errorf("expected unlimited up (0), got %d", up)
	}
}

func TestSetRateLimits(t *testing.T) {
	s := NewForTesting()
	s.SetRateLimits(1<<20, 512<<10) // 1 MB/s down, 512 KB/s up
	down, up := s.RateLimits()
	if down != 1<<20 {
		t.Errorf("down: want %d, got %d", 1<<20, down)
	}
	if up != 512<<10 {
		t.Errorf("up: want %d, got %d", 512<<10, up)
	}
}

func TestSetRateLimits_Unlimited(t *testing.T) {
	s := NewForTesting()
	s.SetRateLimits(1<<20, 512<<10)
	s.SetRateLimits(0, 0)
	down, up := s.RateLimits()
	if down != 0 || up != 0 {
		t.Errorf("expected unlimited (0) after setting 0, got down=%d up=%d", down, up)
	}
}

func TestFavorites_Nil(t *testing.T) {
	s := NewForTesting()
	if f := s.Favorites(); f != nil {
		t.Fatal("expected nil FavoritesStore on fresh NewForTesting")
	}
}

func TestRegisterUnregisterDownload_Empty(t *testing.T) {
	s := NewForTesting()
	s.RegisterDownload("")
	if len(s.downloads) != 0 {
		t.Errorf("empty name should not register")
	}
}

func TestMethodsOnNilStreamer(t *testing.T) {
	var s *Streamer
	if f := s.Favorites(); f != nil {
		t.Error("Favorites() on nil Streamer should return nil")
	}
}

func TestParseMagnet_Invalid(t *testing.T) {
	s := &Streamer{}
	if _, _, err := s.ParseMagnet(""); err == nil {
		t.Error("expected error for empty magnet")
	}
}

func TestFmtBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1048576, "1.00 MB"},
		{1073741824, "1.00 GB"},
	}
	for _, tc := range cases {
		got := fmtBytes(tc.n)
		if got != tc.want {
			t.Errorf("fmtBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestPriorityLabelRoundTrip(t *testing.T) {
	tests := []struct {
		label string
		prio  string
	}{
		{"none", "none"},
		{"low", "low"},
		{"normal", "normal"},
		{"high", "high"},
		{"", "normal"},
	}
	for _, tc := range tests {
		prio, ok := priorityFromLabel(tc.label)
		if !ok {
			t.Errorf("priorityFromLabel(%q) should be OK", tc.label)
			continue
		}
		got := labelFromPriority(prio)
		if got != tc.prio {
			t.Errorf("labelFromPriority(priorityFromLabel(%q)) = %q, want %q", tc.label, got, tc.prio)
		}
	}
}

func TestPriorityFromLabel_Invalid(t *testing.T) {
	if _, ok := priorityFromLabel("invalid"); ok {
		t.Error("expected invalid for unknown label")
	}
}

func TestIsMagnet(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		{"magnet:?xt=urn:btih:abc", true},
		{"MAGNET:?xt=urn:btih:abc", true},
		{"http://example.com/file.torrent", false},
		{"https://example.com/file.torrent", false},
	}
	for _, tc := range cases {
		got := isMagnet(strings.ToLower(tc.src[:min(16, len(tc.src))]), tc.src)
		if got != tc.want {
			t.Errorf("isMagnet(%q) = %v, want %v", tc.src, got, tc.want)
		}
	}
}
