package transcode

import "testing"

func TestShouldUseHWDecode(t *testing.T) {
	cases := []struct {
		sourceCodec, encoder string
		want                 bool
	}{
		{"hevc", "h264_nvenc", true},
		{"h265", "h264_nvenc", true},
		{"vp9", "h264_nvenc", true},
		{"av1", "h264_nvenc", true},
		{"h264", "h264_nvenc", false}, // already H.264, CPU decode fine
		{"hevc", "libx264", false},    // CPU encoder, no HW decode needed
		{"", "h264_nvenc", false},     // unknown source
	}
	for _, tc := range cases {
		if got := shouldUseHWDecode(tc.sourceCodec, tc.encoder); got != tc.want {
			t.Errorf("shouldUseHWDecode(%q, %q) = %v, want %v",
				tc.sourceCodec, tc.encoder, got, tc.want)
		}
	}
}

func TestHwaccelDecodeArgsByBackend(t *testing.T) {
	cases := map[string]string{
		"h264_nvenc":        "cuda",
		"hevc_nvenc":        "cuda",
		"h264_vaapi":        "vaapi",
		"h264_qsv":          "qsv",
		"h264_videotoolbox": "videotoolbox",
		"libx264":           "",
	}
	for encoder, wantArg := range cases {
		args := hwaccelDecodeArgs(encoder)
		if wantArg == "" {
			if args != nil {
				t.Errorf("expected nil args for %s, got %v", encoder, args)
			}
			continue
		}
		joined := ""
		for _, a := range args {
			joined += a + " "
		}
		if !contains(joined, wantArg) {
			t.Errorf("encoder %s: expected hwaccel %s, got %v", encoder, wantArg, args)
		}
	}
}

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

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
