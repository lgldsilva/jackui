// Package clientcaps decides direct-play vs transcode from the codecs a
// client says it can play natively (issue #201). Native clients (Android TV,
// native HLS players) probe with ?caps=mkv,hevc,ac3,... and get a
// playMethod of direct|transcode plus the reason; without caps every caller
// keeps the browser whitelist decision.
package clientcaps

import (
	"testing"
)

func TestParse(t *testing.T) {
	got := Parse(" MKV , hevc ,  ac3,, ")
	want := map[string]bool{"mkv": true, "hevc": true, "ac3": true}
	for token := range want {
		if !got[token] {
			t.Errorf("missing token %q in %v", token, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("Parse = %v, want exactly %v", got, want)
	}
	if len(Parse("")) != 0 {
		t.Error("empty query must yield empty set")
	}
}

// Decide mirrors the browser whitelist when caps is nil, and extends it with
// the client's advertised tokens otherwise. The reason names the FIRST
// blocking dimension, in container → vcodec → acodec order (same convention
// as the existing browser-side classifiers).
func TestDecide(t *testing.T) {
	cases := []struct {
		name            string
		container, v, a string
		caps            map[string]bool
		wantMethod      string
		wantReason      string
	}{
		{"nil caps browser safe", "mp4", "h264", "aac", nil, "direct", ""},
		{"nil caps mkv blocks", "matroska", "h264", "aac", nil, "transcode", "container=matroska"},
		{"nil caps hevc blocks", "mp4", "hevc", "aac", nil, "transcode", "vcodec=hevc"},
		{"nil caps ac3 blocks", "mp4", "h264", "ac3", nil, "transcode", "acodec=ac3"},
		{"empty container does not gate", "", "h264", "aac", nil, "direct", ""},
		{"caps unlock mkv alias", "matroska", "hevc", "dts", map[string]bool{"mkv": true, "hevc": true, "dts": true}, "direct", ""},
		{"caps unlock literal container", "mkv", "h264", "aac", map[string]bool{"mkv": true}, "direct", ""},
		{"missing container cap blocks", "matroska", "hevc", "ac3", map[string]bool{"hevc": true, "ac3": true}, "transcode", "container=matroska"},
		{"missing vcodec cap blocks", "mkv", "hevc", "ac3", map[string]bool{"mkv": true, "ac3": true}, "transcode", "vcodec=hevc"},
		{"missing acodec cap blocks", "mp4", "h264", "ac3", map[string]bool{"aac": true}, "transcode", "acodec=ac3"},
		{"first blocking dimension wins", "matroska", "hevc", "ac3", map[string]bool{}, "transcode", "container=matroska"},
		{"subtitle tokens are accepted but unused", "mp4", "h264", "aac", map[string]bool{"ass": true, "pgs": true}, "direct", ""},
		{"truehd needs its own cap", "mp4", "h264", "truehd", nil, "transcode", "acodec=truehd"},
		{"truehd unlocked", "mp4", "h264", "truehd", map[string]bool{"truehd": true}, "direct", ""},
	}
	for _, tc := range cases {
		method, reason := Decide(tc.container, tc.v, tc.a, tc.caps)
		if method != tc.wantMethod {
			t.Errorf("%s: method = %q, want %q", tc.name, method, tc.wantMethod)
		}
		if reason != tc.wantReason {
			t.Errorf("%s: reason = %q, want %q", tc.name, reason, tc.wantReason)
		}
	}
}

func TestDecideCapsTokensCaseInsensitive(t *testing.T) {
	method, _ := Decide("matroska", "h264", "aac", Parse("MKV"))
	if method != "direct" {
		t.Errorf("method = %q, want direct", method)
	}
}
