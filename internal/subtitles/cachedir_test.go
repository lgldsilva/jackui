package subtitles

import "testing"

// TestClientCacheDir pins the accessor the transcode burn-in wiring uses
// (routes.go passes it into handlers.TranscodeStream): nil-safe, and empty
// when caching is disabled.
func TestClientCacheDir(t *testing.T) {
	if got := (*Client)(nil).CacheDir(); got != "" {
		t.Errorf("nil client CacheDir() = %q, want \"\"", got)
	}
	if got := New("", "", "", "").CacheDir(); got != "" {
		t.Errorf("caching-disabled client CacheDir() = %q, want \"\"", got)
	}
	dir := t.TempDir()
	if got := New("", "", "", dir).CacheDir(); got != dir {
		t.Errorf("CacheDir() = %q, want %q", got, dir)
	}
}
