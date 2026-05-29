package streamer

import "testing"

func TestParseProbeOutput_Empty(t *testing.T) {
	_, err := parseProbeOutput([]byte(`{}`))
	if err != nil {
		t.Fatalf("parseProbeOutput(empty): %v", err)
	}
}

func TestParseProbeOutput_InvalidJSON(t *testing.T) {
	_, err := parseProbeOutput([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseProbeOutput_Basic(t *testing.T) {
	out := []byte(`{
		"streams": [
			{
				"index": 0,
				"codec_type": "audio",
				"codec_name": "aac",
				"channels": 2,
				"tags": {"language": "eng", "title": "English 5.1"},
				"disposition": {"default": 1, "forced": 0}
			},
			{
				"index": 1,
				"codec_type": "subtitle",
				"codec_name": "subrip",
				"tags": {"language": "por", "title": "Portuguese"},
				"disposition": {"default": 0, "forced": 1}
			}
		],
		"format": {"duration": "6543.210"}
	}`)

	r, err := parseProbeOutput(out)
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}

	if len(r.Audio) != 1 {
		t.Fatalf("expected 1 audio track, got %d", len(r.Audio))
	}
	a := r.Audio[0]
	if a.Index != 0 || a.Codec != "aac" || a.Channels != 2 || a.Language != "eng" || a.Title != "English 5.1" || !a.Default || a.Forced {
		t.Errorf("audio track malformed: %+v", a)
	}

	if len(r.Subtitles) != 1 {
		t.Fatalf("expected 1 subtitle track, got %d", len(r.Subtitles))
	}
	s := r.Subtitles[0]
	if s.Index != 1 || s.Codec != "subrip" || s.Language != "por" || s.Default || !s.Forced || s.Image {
		t.Errorf("subtitle track malformed: %+v", s)
	}

	if r.DurationSec != 6543.21 {
		t.Errorf("duration = %f, want 6543.21", r.DurationSec)
	}
}

func TestParseProbeOutput_ImageSubtitles(t *testing.T) {
	cases := []struct {
		codec string
		image bool
	}{
		{"hdmv_pgs_subtitle", true},
		{"dvd_subtitle", true},
		{"dvdsub", true},
		{"pgssub", true},
		{"subrip", false},
		{"ass", false},
		{"webvtt", false},
	}
	for _, tc := range cases {
		out := []byte(`{
			"streams": [{
				"index": 0,
				"codec_type": "subtitle",
				"codec_name": "` + tc.codec + `",
				"tags": {},
				"disposition": {"default": 0, "forced": 0}
			}],
			"format": {}
		}`)
		r, err := parseProbeOutput(out)
		if err != nil {
			t.Fatalf("codec %q: %v", tc.codec, err)
		}
		if len(r.Subtitles) != 1 {
			t.Fatalf("codec %q: expected 1 subtitle", tc.codec)
		}
		if r.Subtitles[0].Image != tc.image {
			t.Errorf("codec %q: Image=%v, want %v", tc.codec, r.Subtitles[0].Image, tc.image)
		}
	}
}

func TestParseProbeOutput_NoDuration(t *testing.T) {
	out := []byte(`{
		"streams": [],
		"format": {}
	}`)
	r, err := parseProbeOutput(out)
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if r.DurationSec != 0 {
		t.Errorf("expected 0 duration, got %f", r.DurationSec)
	}
}

func TestIsImageSubtitle(t *testing.T) {
	cases := []struct {
		codec string
		want  bool
	}{
		{"hdmv_pgs_subtitle", true},
		{"dvd_subtitle", true},
		{"dvdsub", true},
		{"pgssub", true},
		{"xsub", true},
		{"subrip", false},
		{"ass", false},
		{"webvtt", false},
		{"h264", false},
		{"aac", false},
	}
	for _, tc := range cases {
		got := isImageSubtitle(tc.codec)
		if got != tc.want {
			t.Errorf("isImageSubtitle(%q) = %v, want %v", tc.codec, got, tc.want)
		}
	}
}
