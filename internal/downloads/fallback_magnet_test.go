package downloads

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFallbackMagnet(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		infoHash string
		want     string
		wantOK   bool
	}{
		{"http url + hash", "http://jackett/dl/x.torrent", "abc123", "magnet:?xt=urn:btih:abc123", true},
		{"https url + hash", "https://idx/dl/x.torrent?apikey=k", "DEAD", "magnet:?xt=urn:btih:DEAD", true},
		{"already a magnet", "magnet:?xt=urn:btih:abc123", "abc123", "", false},
		{"no info hash", "http://jackett/dl/x.torrent", "", "", false},
		{"non-http source", "ftp://x/y", "abc123", "", false},
		{"url with surrounding space", "  http://idx/x.torrent  ", "h", "magnet:?xt=urn:btih:h", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := fallbackMagnet(tc.src, tc.infoHash)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("fallbackMagnet(%q,%q) = (%q,%v), want (%q,%v)", tc.src, tc.infoHash, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// A magnet source that fails to load and is NOT an http URL gets no fallback —
// the original error is returned unchanged (no second attempt).
func Test_dlw_EnsureActiveWithFallback_NoFallbackForMagnet(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d := Download{UserID: 1, InfoHash: "badhash", Magnet: "m:hash"} // "m:hash" not a valid magnet, not http
	_, err := w.ensureActiveWithFallback(ctx, &d)
	if err == nil {
		t.Fatal("expected error for invalid magnet")
	}
	if strings.Contains(err.Error(), "fallback") {
		t.Fatalf("should not have attempted fallback for a non-http source: %v", err)
	}
}

// An http(s) .torrent source that fails (blocked/404) WITH a known info_hash
// triggers the bare-magnet fallback; here the fallback magnet is itself invalid
// (bad hash) so the call still errors — but the error proves the fallback ran.
func Test_dlw_EnsureActiveWithFallback_HttpTriggersFallback(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Loopback is refused fast by the SSRF guard (no network), so the primary
	// EnsureActive fails immediately; the info_hash drives the fallback attempt.
	d := Download{UserID: 1, InfoHash: "badhash", Magnet: "http://127.0.0.1:1/dl/expired.torrent"}
	_, err := w.ensureActiveWithFallback(ctx, &d)
	if err == nil {
		t.Fatal("expected error (both primary and fallback fail)")
	}
	if !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("expected the fallback to have been attempted, got: %v", err)
	}
}
