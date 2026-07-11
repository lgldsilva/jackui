package transcode

import (
	"strings"
	"testing"
)

func heights(vs []Variant) []int {
	out := make([]int, len(vs))
	for i, v := range vs {
		out[i] = v.Height
	}
	return out
}

func TestVariantLadder(t *testing.T) {
	cases := []struct {
		name     string
		src      int
		want     []int // heights, highest→lowest
		default_ bool  // single legacy sentinel expected
	}{
		{"4k", 2160, []int{1080, 720, 480}, false},
		{"4k+", 4320, []int{1080, 720, 480}, false},
		{"1080p exact (CA-2.1 ≥2)", 1080, []int{1080, 720}, false},
		{"1440p", 1440, []int{1080, 720}, false},
		{"720p single no upscale", 720, []int{720}, false},
		{"480p single", 480, []int{480}, false},
		{"unknown → default sentinel", 0, []int{0}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := variantLadder(c.src)
			gh := heights(got)
			if len(gh) != len(c.want) {
				t.Fatalf("ladder(%d) heights = %v, want %v", c.src, gh, c.want)
			}
			for i := range c.want {
				if gh[i] != c.want[i] {
					t.Errorf("ladder(%d)[%d] = %d, want %d", c.src, i, gh[i], c.want[i])
				}
			}
			// Ordering: strictly descending.
			for i := 1; i < len(got); i++ {
				if !got[i-1].IsDefault() && got[i].Height >= got[i-1].Height {
					t.Errorf("ladder(%d) not descending: %v", c.src, gh)
				}
			}
			if got[0].IsDefault() != c.default_ {
				t.Errorf("ladder(%d) IsDefault = %v, want %v", c.src, got[0].IsDefault(), c.default_)
			}
		})
	}
}

// CA-2.1: fonte ≥1080p produz ≥2 variantes.
func TestVariantLadderCA21(t *testing.T) {
	for _, src := range []int{1080, 1440, 2160, 4320} {
		if n := len(variantLadder(src)); n < 2 {
			t.Errorf("CA-2.1: fonte %dp deve ter ≥2 variantes, tem %d", src, n)
		}
	}
}

func TestVariantAttributes(t *testing.T) {
	cases := []struct {
		h          int
		wantLevel  string
		wantCodecs string
	}{
		{1080, "4.0", "avc1.4d4028,mp4a.40.2"},
		{720, "3.1", "avc1.4d401f,mp4a.40.2"},
		{480, "3.0", "avc1.4d401e,mp4a.40.2"},
	}
	for _, c := range cases {
		v := mkVariant(c.h)
		if v.LevelStr() != c.wantLevel {
			t.Errorf("%dp LevelStr = %q, want %q", c.h, v.LevelStr(), c.wantLevel)
		}
		if v.Codecs() != c.wantCodecs {
			t.Errorf("%dp Codecs = %q, want %q", c.h, v.Codecs(), c.wantCodecs)
		}
		if v.Bandwidth() <= 0 {
			t.Errorf("%dp Bandwidth must be >0, got %d", c.h, v.Bandwidth())
		}
	}
	// Bandwidth desce com a resolução (ABR coerente).
	if mkVariant(720).Bandwidth() >= mkVariant(1080).Bandwidth() {
		t.Error("720p BANDWIDTH deve ser < 1080p")
	}
	if mkVariant(480).Bandwidth() >= mkVariant(720).Bandwidth() {
		t.Error("480p BANDWIDTH deve ser < 720p")
	}
}

// O default sentinel (Height 0) → level 5.2, para casar com o -level:v legado.
func TestVariantDefaultSentinelLevel(t *testing.T) {
	v := variantLadder(0)[0]
	if !v.IsDefault() || v.LevelStr() != "5.2" {
		t.Errorf("default sentinel = %+v (LevelStr %q), want Height 0 / L5.2", v, v.LevelStr())
	}
}

// Uma rung de variante muda scale/level/maxrate no ffmpeg args; o default
// (Height 0) permanece byte-a-byte o comando legado (cap 1080, L5.2, sem maxrate).
func TestEncodeSpecVariantArgs(t *testing.T) {
	base := func(v Variant) string {
		spec := &encodeSpec{
			dir: "/tmp/x", inputURL: "http://127.0.0.1:1/source", encoder: "libx264",
			ffmpegPath: "ffmpeg", vod: true,
			variantHeight: v.Height, variantBitrateK: v.VBitrateK, variantLevel: v.Level,
		}
		return strings.Join(spec.args(0), " ")
	}

	// 720p rung.
	got720 := base(mkVariant(720))
	for _, want := range []string{"min(720,ih)", "-level:v 3.1", "-maxrate 2800k", "-bufsize 5600k"} {
		if !strings.Contains(got720, want) {
			t.Errorf("720p args missing %q; got:\n%s", want, got720)
		}
	}

	// Default sentinel (legacy): cap 1080, level 5.2, NO maxrate.
	gotDef := strings.Join((&encodeSpec{
		dir: "/tmp/x", inputURL: "http://127.0.0.1:1/source", encoder: "libx264",
		ffmpegPath: "ffmpeg", vod: true,
	}).args(0), " ")
	if !strings.Contains(gotDef, "min(1080,ih)") || !strings.Contains(gotDef, "-level:v 5.2") {
		t.Errorf("default must keep cap 1080 + L5.2; got:\n%s", gotDef)
	}
	if strings.Contains(gotDef, "-maxrate") {
		t.Errorf("default (legacy) must NOT add -maxrate; got:\n%s", gotDef)
	}
}

// Scale filter por backend usa a altura da rung (VAAPI/QSV/CPU).
func TestVideoScaleFilterH(t *testing.T) {
	cases := []struct{ enc, want string }{
		{"libx264", "min(720,ih)"},
		{"h264_nvenc", "min(720,ih)"},
		{"h264_vaapi", `min(720\,ih)`},
		{"h264_qsv", `min(720\,ih)`},
	}
	for _, c := range cases {
		if got := videoScaleFilterH(c.enc, 720); !strings.Contains(got, c.want) {
			t.Errorf("videoScaleFilterH(%q,720) = %q, want to contain %q", c.enc, got, c.want)
		}
	}
	// maxH ≤ 0 → default 1080 (não regride o wrapper legado).
	if got := videoScaleFilterH("libx264", 0); !strings.Contains(got, "min(1080,ih)") {
		t.Errorf("videoScaleFilterH(_,0) must fall back to 1080; got %q", got)
	}
}
