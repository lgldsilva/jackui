package transcode

import (
	"strings"
	"testing"
)

func TestEncoderPresetArgsHasReasonableDefaults(t *testing.T) {
	if args := encoderPresetArgs("h264_nvenc"); len(args) == 0 {
		t.Error("nvenc should have preset args")
	}
	if args := encoderPresetArgs("libx264"); len(args) == 0 {
		t.Error("libx264 should have preset args")
	}
	if args := encoderPresetArgs("unknown_codec"); args != nil {
		t.Errorf("unknown codec should return nil, got %v", args)
	}
}

func TestContainerMime(t *testing.T) {
	cases := map[string]string{
		"mp4":      "video/mp4",
		"matroska": "video/x-matroska",
		"webm":     "video/webm",
		"unknown":  "application/octet-stream",
	}
	for c, want := range cases {
		if got := containerMime(c); got != want {
			t.Errorf("containerMime(%q) = %q, want %q", c, got, want)
		}
	}
}

func TestEncoderForCodec(t *testing.T) {
	caps := &Capabilities{Preferred: "h264_nvenc", PreferredHE: "hevc_nvenc"}
	cases := []struct {
		videoCodec string
		want       string
	}{
		{"h264", "h264_nvenc"},
		{"hevc", "hevc_nvenc"},
		{"", ""},        // no video transcode
		{"av1", ""},     // unknown codec → empty
		{"unknown", ""}, // default branch
	}
	for _, c := range cases {
		if got := encoderForCodec(caps, c.videoCodec); got != c.want {
			t.Errorf("encoderForCodec(%q) = %q, want %q", c.videoCodec, got, c.want)
		}
	}
}

func TestHWDecodeArgsFor(t *testing.T) {
	cases := []struct {
		encoder     string
		wantContain string // a token that must be present; "" means expect nil/empty
	}{
		{"h264_vaapi", "vaapi"},
		{"hevc_vaapi", "vaapi"},
		{"h264_nvenc", "cuda"},
		{"hevc_nvenc", "cuda"},
		{"h264_qsv", "qsv"},
		{"libx264", ""},           // CPU → software decode, no args
		{"libx265", ""},           // CPU
		{"h264_videotoolbox", ""}, // not in switch → nil
		{"", ""},
	}
	for _, c := range cases {
		got := hwDecodeArgsFor(c.encoder)
		if c.wantContain == "" {
			if got != nil {
				t.Errorf("hwDecodeArgsFor(%q) = %v, want nil", c.encoder, got)
			}
			continue
		}
		if !strings.Contains(strings.Join(got, " "), c.wantContain) {
			t.Errorf("hwDecodeArgsFor(%q) = %v, want to contain %q", c.encoder, got, c.wantContain)
		}
	}
}

func TestVideoScaleFilter(t *testing.T) {
	cases := []struct {
		encoder     string
		wantContain string
	}{
		{"h264_vaapi", "scale_vaapi"},
		{"hevc_vaapi", "scale_vaapi"},
		{"h264_qsv", "scale_qsv"},
		{"h264_nvenc", "format=yuv420p"},        // default branch (sw scale)
		{"libx264", "format=yuv420p"},           // default branch
		{"h264_videotoolbox", "format=yuv420p"}, // default branch
	}
	for _, c := range cases {
		if got := videoScaleFilter(c.encoder); !strings.Contains(got, c.wantContain) {
			t.Errorf("videoScaleFilter(%q) = %q, want to contain %q", c.encoder, got, c.wantContain)
		}
	}
}
