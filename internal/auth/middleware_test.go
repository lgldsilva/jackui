package auth

import "testing"

// Guards the ?token= allow-list. SSE search + media elements can't set an
// Authorization header, so they MUST be media paths; sensitive JSON endpoints
// must NOT be (the token would leak into access logs / downloaded files).
func TestIsMediaPath(t *testing.T) {
	media := []string{
		"/api/stream/abc/0",
		"/api/stream/hls/abc/0/index.m3u8",
		"/api/stream/art/abc",
		"/api/subtitles/download/123",
		"/api/local/file",
		"/api/local/thumb",
		"/api/search/stream",
		"/api/search/stream?q=x&token=y",
		// Universal viewer: comic pages / EPUB chapters / archive images load
		// via <img>/<iframe>, headerless by nature.
		"/api/preview/archive",
		"/api/preview/comic/page",
		"/api/preview/epub/chapter",
	}
	for _, p := range media {
		if !isMediaPath(p) {
			t.Errorf("expected %q to be a media path (needs ?token=)", p)
		}
	}
	notMedia := []string{
		"/api/search",   // axios, sends Bearer
		"/api/config",   // sensitive — token must NOT be allowed in query
		"/api/download",
		"/api/library/1",
		"/api/stream",   // exact, no trailing slash
	}
	for _, p := range notMedia {
		if isMediaPath(p) {
			t.Errorf("expected %q to NOT be a media path", p)
		}
	}
}
