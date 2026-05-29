package transcode

import "testing"

func TestCached_Nil(t *testing.T) {
	cacheMu.Lock()
	prev := cached
	cached = nil
	cacheMu.Unlock()
	defer func() {
		cacheMu.Lock()
		cached = prev
		cacheMu.Unlock()
	}()
	if c := Cached(); c != nil {
		t.Error("Cached() should be nil when not probed")
	}
}

func TestCapabilities_String(t *testing.T) {
	c := &Capabilities{
		FFmpegPath: "/usr/bin/ffmpeg",
		OS:         "linux",
		Preferred:  "libx264",
	}
	s := c.String()
	if s == "" {
		t.Error("String() should not be empty")
	}
}

func TestParseCodecList(t *testing.T) {
	out := []byte(`Encoders:
 V..... = Video
 A..... = Audio
 S..... = Subtitle
 ------
 V....D h264_nvenc           NVIDIA NVENC H.264 encoder
 V....D libx264              libx264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10
 V....D libx265              libx265 H.265 / HEVC
 A....D aac                  AAC (Advanced Audio Coding)
`)
	m := parseCodecList(out)
	for _, want := range []string{"h264_nvenc", "libx264", "libx265", "aac"} {
		if !m[want] {
			t.Errorf("missed %q in parsed list", want)
		}
	}
	if m["Encoders"] {
		t.Error("parser picked up header line")
	}
}

func TestPickPreferredFavorsNVIDIA(t *testing.T) {
	encs := []Encoder{
		{ID: "libx264", Codec: "h264", Backend: "cpu", Functional: true},
		{ID: "h264_vaapi", Codec: "h264", Backend: "amd-vaapi", Functional: true},
		{ID: "h264_nvenc", Codec: "h264", Backend: "nvidia", Functional: true},
	}
	if got := pickPreferred(encs, "h264"); got != "h264_nvenc" {
		t.Errorf("expected h264_nvenc, got %s", got)
	}
}

func TestPickPreferredFallsBackToCPU(t *testing.T) {
	encs := []Encoder{
		{ID: "libx264", Codec: "h264", Backend: "cpu", Functional: true},
		{ID: "h264_nvenc", Codec: "h264", Backend: "nvidia", Functional: false}, // not functional → skipped
	}
	if got := pickPreferred(encs, "h264"); got != "libx264" {
		t.Errorf("expected libx264 fallback, got %s", got)
	}
}

func TestPickPreferredNoneFunctional(t *testing.T) {
	encs := []Encoder{
		{ID: "h264_nvenc", Codec: "h264", Backend: "nvidia", Functional: false},
		{ID: "libx264", Codec: "h264", Backend: "cpu", Functional: false},
	}
	if got := pickPreferred(encs, "h264"); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}

func TestPickPreferredFilterByCodec(t *testing.T) {
	encs := []Encoder{
		{ID: "h264_nvenc", Codec: "h264", Backend: "nvidia", Functional: true},
		{ID: "hevc_nvenc", Codec: "hevc", Backend: "nvidia", Functional: true},
	}
	if got := pickPreferred(encs, "hevc"); got != "hevc_nvenc" {
		t.Errorf("hevc selection: got %s", got)
	}
}
