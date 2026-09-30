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
		w, h     int
		want     []int // heights, highest→lowest
		default_ bool  // single legacy sentinel expected
	}{
		{"4k", 3840, 2160, []int{1080, 720, 480}, false},
		{"4k+", 7680, 4320, []int{1080, 720, 480}, false},
		{"1080p exact (CA-2.1 ≥2)", 1920, 1080, []int{1080, 720}, false},
		{"1440p", 2560, 1440, []int{1080, 720}, false},
		{"720p single no upscale", 1280, 720, []int{720}, false},
		{"480p single", 854, 480, []int{480}, false},
		{"unknown → default sentinel", 0, 0, []int{0}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := variantLadder(c.w, c.h)
			gh := heights(got)
			if len(gh) != len(c.want) {
				t.Fatalf("ladder(%d,%d) heights = %v, want %v", c.w, c.h, gh, c.want)
			}
			for i := range c.want {
				if gh[i] != c.want[i] {
					t.Errorf("ladder(%d,%d)[%d] = %d, want %d", c.w, c.h, i, gh[i], c.want[i])
				}
			}
			// Ordering: strictly descending.
			for i := 1; i < len(got); i++ {
				if !got[i-1].IsDefault() && got[i].Height >= got[i-1].Height {
					t.Errorf("ladder(%d,%d) not descending: %v", c.w, c.h, gh)
				}
			}
			if got[0].IsDefault() != c.default_ {
				t.Errorf("ladder(%d,%d) IsDefault = %v, want %v", c.w, c.h, got[0].IsDefault(), c.default_)
			}
		})
	}
}

// CA-2.1: a source ≥1080p produces ≥2 variants. Uses the EXPORTED VariantLadder
// wrapper (what the handlers call).
func TestVariantLadderCA21(t *testing.T) {
	for _, h := range []int{1080, 1440, 2160, 4320} {
		if n := len(VariantLadder(h*16/9, h)); n < 2 {
			t.Errorf("CA-2.1: %dp source must have ≥2 variants, has %d", h, n)
		}
	}
}

// A very short source (<480) uses the minimum bitrate/level, no upscale.
func TestVariantLadderLowRes(t *testing.T) {
	l := VariantLadder(640, 360)
	if len(l) != 1 || l[0].Height != 360 {
		t.Fatalf("360p → %+v, want 1 rung @360", l)
	}
	if l[0].VBitrateK != 800 || l[0].Level != 30 {
		t.Errorf("360p rung = %+v, want VBitrateK 800 / Level 30", l[0])
	}
}

// The level must fit the whole OUTPUT frame, not the height tier: a 2.39:1
// cinemascope 1920×804 carries ~6120 macroblocks, which L3.1 (3600) forbids —
// the exact "InitializeEncoder failed: invalid param (8): Invalid Level"
// NVENC abort seen in production for a sub-1080p widescreen source.
func TestVariantLadderWidescreenLevels(t *testing.T) {
	cases := []struct {
		name          string
		w, h          int
		rung, wantLvl int
	}{
		{"cinemascope 1080-bluray (prod failure)", 1920, 804, 804, 40},
		{"4k cinemascope, top rung", 3840, 1608, 1080, 50},
		{"4k cinemascope, 720 rung", 3840, 1608, 720, 40},
		{"16:9 720p stays L3.1", 1280, 720, 720, 31},
		{"16:9 1080p stays L4.0", 1920, 1080, 1080, 40},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, v := range variantLadder(c.w, c.h) {
				if v.Height != c.rung {
					continue
				}
				if v.Level != c.wantLvl {
					t.Errorf("rung %d of %dx%d: Level = %d, want %d", c.rung, c.w, c.h, v.Level, c.wantLvl)
				}
				return
			}
			t.Fatalf("rung %d not found in ladder(%d,%d)", c.rung, c.w, c.h)
		})
	}
}

func TestLevelIdcForSize(t *testing.T) {
	cases := []struct {
		name    string
		w, h    int
		wantLvl int
	}{
		{"16:9 720 exact L3.1 budget", 1280, 720, 31},
		{"cinemascope 804 needs L4.0", 1920, 804, 40},
		{"tiny frame fits L3.0", 640, 360, 30},
		{"4:3 1440×1080 needs L4.0", 1440, 1080, 40},
		{"unknown width falls back to height map", 0, 720, 31},
		{"extreme width tops out", 7680, 480, 50},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := levelIdcForSize(c.w, c.h); got != c.wantLvl {
				t.Errorf("levelIdcForSize(%d,%d) = %d, want %d", c.w, c.h, got, c.wantLvl)
			}
		})
	}
}

func TestVariantWidth(t *testing.T) {
	cases := []struct {
		name           string
		srcW, srcH, vh int
		want           int
	}{
		{"16:9 no rounding", 1920, 1080, 720, 1280},
		{"odd width rounds up to even", 853, 480, 480, 854},
		{"2.39:1 1080 rung", 3840, 1608, 1080, 2580},
		{"unknown dims → 0", 0, 0, 720, 0},
		{"zero height → 0", 1920, 1080, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := VariantWidth(c.srcW, c.srcH, c.vh); got != c.want {
				t.Errorf("VariantWidth(%d,%d,%d) = %d, want %d", c.srcW, c.srcH, c.vh, got, c.want)
			}
		})
	}
}

func TestVariantAttributes(t *testing.T) {
	// 16:9 widths: the level comes from the output frame (see the widescreen
	// cases above) — these pin the classic square-ish tiers.
	cases := []struct {
		w, h       int
		wantLevel  string
		wantCodecs string
	}{
		{1920, 1080, "4.0", "avc1.4d4028,mp4a.40.2"},
		{1280, 720, "3.1", "avc1.4d401f,mp4a.40.2"},
		// 854×480@30fps exceeds L3.0's MaxMBPS budget (48600 > 40500), so the
		// spec-correct minimum is L3.1 — the old height-only L3.0 was invalid.
		{854, 480, "3.1", "avc1.4d401f,mp4a.40.2"},
	}
	for _, c := range cases {
		v := mkVariant(c.w, c.h, c.h)
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
	// Bandwidth descends with resolution (coherent ABR).
	if mkVariant(1280, 720, 720).Bandwidth() >= mkVariant(1920, 1080, 1080).Bandwidth() {
		t.Error("720p BANDWIDTH must be < 1080p")
	}
	if mkVariant(854, 480, 480).Bandwidth() >= mkVariant(1280, 720, 720).Bandwidth() {
		t.Error("480p BANDWIDTH must be < 720p")
	}
}

// O default sentinel (Height 0) → level 5.2, para casar com o -level:v legado.
func TestVariantDefaultSentinelLevel(t *testing.T) {
	v := variantLadder(0, 0)[0]
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

	// 720p rung (16:9 source).
	got720 := base(mkVariant(1280, 720, 720))
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
	// maxH ≤ 0 → default 1080 (doesn't regress the legacy wrapper).
	if got := videoScaleFilterH("libx264", 0); !strings.Contains(got, "min(1080,ih)") {
		t.Errorf("videoScaleFilterH(_,0) must fall back to 1080; got %q", got)
	}
}
