package transcode

import "testing"

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
