package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lgldsilva/jackui/internal/clientcaps"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// Caps-aware probe responses (issue #201): ?caps= adds playMethod + reason;
// without the parameter the response is byte-identical to the legacy body.

func TestStreamProbeResponseFor_NoCapsUnchanged(t *testing.T) {
	probe := streamer.ProbeResult{
		VideoCodec: "h264", Container: "mp4", AudioCodec: "aac", NeedsTranscode: false,
	}
	resp := streamProbeResponseFor(probe, "")
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if containsField(string(out), "playMethod") {
		t.Fatalf("no caps must omit playMethod: %s", out)
	}
	if containsField(string(out), `"reason"`) {
		t.Fatalf("no caps must omit reason: %s", out)
	}
}

func TestStreamProbeResponseFor_WithCaps(t *testing.T) {
	probe := streamer.ProbeResult{
		VideoCodec: "hevc", Container: "matroska", AudioCodec: "dts", NeedsTranscode: true,
		TranscodeReason: "container=matroska",
	}

	resp := streamProbeResponseFor(probe, "mkv,hevc,dts")
	out := marshal(t, resp)
	if got := fieldValue(t, out, "playMethod"); got != clientcaps.PlayDirect {
		t.Fatalf("playMethod = %q, want direct (all caps advertised)", got)
	}
	if containsField(string(out), `"reason"`) {
		t.Fatalf("direct play must omit reason: %s", out)
	}

	resp = streamProbeResponseFor(probe, "hevc")
	out = marshal(t, resp)
	if got := fieldValue(t, out, "playMethod"); got != clientcaps.PlayTranscode {
		t.Fatalf("playMethod = %q, want transcode (container missing)", got)
	}
	if got := fieldValue(t, out, "reason"); got != "container=matroska" {
		t.Fatalf("reason = %q, want container=matroska", got)
	}
}

func TestStreamProbeResponseFor_BlankCapsUnchanged(t *testing.T) {
	probe := streamer.ProbeResult{VideoCodec: "h264", Container: "mp4", AudioCodec: "aac"}
	resp := streamProbeResponseFor(probe, "   ")
	out := marshal(t, resp)
	if containsField(string(out), "playMethod") {
		t.Fatalf("blank caps must omit playMethod: %s", out)
	}
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func containsField(body, field string) bool {
	return strings.Contains(body, field)
}

func fieldValue(t *testing.T, body, field string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	v, _ := m[field].(string)
	return v
}
