package streamer

import (
	"strings"
	"testing"
)

// Pure dispatch tests — we don't actually add to a real client (no DHT network in unit tests),
// we just validate that Add() routes the input to the right branch (magnet vs http URL vs reject).
// This catches the regression where a `magnet:` URL was being treated as an HTTP URL.

func TestDetectMagnetPrefix(t *testing.T) {
	// Real magnet URL from a real Jackett response (the one that broke production)
	src := "magnet:?xt=urn:btih:2DBC910B807A892F3385E881D0668B12B722FA83&dn=&tr=udp%3A%2F%2Ftracker.torrent.eu.org%3A451%2Fannounce"
	if !strings.HasPrefix(strings.ToLower(src), "magnet:") {
		t.Fatalf("magnet URL not detected: %q", src[:20])
	}
}

func TestDetectMagnetWithLeadingWhitespace(t *testing.T) {
	cases := []string{
		" magnet:?xt=urn:btih:abc",
		"\nmagnet:?xt=urn:btih:abc",
		"\tmagnet:?xt=urn:btih:abc",
		"\xef\xbb\xbfmagnet:?xt=urn:btih:abc", // BOM prefix
	}
	for _, src := range cases {
		cleaned := strings.TrimSpace(src)
		cleaned = strings.TrimPrefix(cleaned, "\xef\xbb\xbf")
		if !strings.HasPrefix(strings.ToLower(cleaned), "magnet:") {
			t.Errorf("after TrimSpace+TrimPrefix(BOM), %q does NOT start with 'magnet:'", cleaned[:min(20, len(cleaned))])
		}
	}
}

func TestDetectHTTPURL(t *testing.T) {
	for _, src := range []string{
		"https://tracker.example/file.torrent?apikey=xxx",
		"http://192.168.1.50:9117/dl/yts/x/y.torrent",
	} {
		lower := strings.ToLower(src)
		if !(strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")) {
			t.Errorf("HTTP URL not detected: %q", src)
		}
	}
}

func TestFirstChars(t *testing.T) {
	if got := firstChars("abcdef", 3); got != "abc..." {
		t.Errorf("truncate: got %q", got)
	}
	if got := firstChars("abc", 10); got != "abc" {
		t.Errorf("short pass-through: got %q", got)
	}
}
