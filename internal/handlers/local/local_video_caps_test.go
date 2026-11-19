package local

import (
	"testing"
)

// Caps-aware local play decision (issue #201): with caps the client's own
// codec/container support extends the browser whitelist; nil keeps the
// legacy classifyForBrowser behavior byte-for-byte.

func TestLocalVideoDecision_NilCapsLegacy(t *testing.T) {
	p := localProbe{Container: "matroska", VideoCodec: "h264", AudioCodec: "aac"}
	direct, reason := localVideoDecision(p, nil)
	if direct || reason != "container=matroska" {
		t.Fatalf("direct=%v reason=%q, want false container=matroska", direct, reason)
	}

	p = localProbe{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac"}
	if direct, reason = localVideoDecision(p, nil); !direct || reason != "" {
		t.Fatalf("direct=%v reason=%q, want true empty", direct, reason)
	}
}

func TestLocalVideoDecision_WithCaps(t *testing.T) {
	// TV client: supports everything the web doesn't.
	p := localProbe{Container: "matroska", VideoCodec: "hevc", AudioCodec: "dts"}
	direct, reason := localVideoDecision(p, map[string]bool{"mkv": true, "hevc": true, "dts": true})
	if !direct || reason != "" {
		t.Fatalf("direct=%v reason=%q, want true empty", direct, reason)
	}

	// Only audio unlocked → container still blocks.
	direct, reason = localVideoDecision(p, map[string]bool{"dts": true})
	if direct || reason != "container=matroska" {
		t.Fatalf("direct=%v reason=%q, want false container=matroska", direct, reason)
	}
}
