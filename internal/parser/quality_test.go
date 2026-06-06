package parser

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		title string
		want  Quality
	}{
		{
			"Movie.Title.2023.1080p.BluRay.x265.10bit.HDR.DDP5.1.Atmos-GROUP",
			Quality{Resolution: "1080p", Codec: "x265", Source: "BluRay", Audio: []string{"EAC3", "Atmos"}, Group: "GROUP", Year: 2023, HDR: true, TenBit: true},
		},
		{
			"Show.S04E12.2160p.WEB-DL.HEVC.DV.HDR.DTS-X-SCENE",
			Quality{Resolution: "2160p", Codec: "x265", Source: "WEB-DL", Audio: []string{"DTS:X"}, Group: "SCENE", Season: 4, Episode: 12, HDR: true, DolbyVis: true},
		},
		{
			"Movie 1080p WEBRip x264 AAC-YIFY",
			Quality{Resolution: "1080p", Codec: "x264", Source: "WEBRip", Audio: []string{"AAC"}, Group: "YIFY"},
		},
		{
			"Movie.2019.REPACK.720p.BluRay.x264-NTb",
			Quality{Resolution: "720p", Codec: "x264", Source: "BluRay", Group: "NTb", Year: 2019, Repack: true},
		},
		{
			"Movie.4K.UHD.REMUX.HDR",
			Quality{Resolution: "2160p", HDR: true, Remux: true},
		},
	}

	for _, tc := range cases {
		assertQualityEqual(t, tc.title, Parse(tc.title), tc.want)
	}
}

// Regression: the bare "s" in a title's possessive must NOT read as "Season N",
// and a year-like number in the title must not beat the real release year.
func TestParse_FalsePositives(t *testing.T) {
	cases := []struct {
		title       string
		wantSeason  int
		wantYear    int
	}{
		{"Ocean's 11 2001 1080p BluRay x264-YIFY", 0, 2001},   // "s 11" ≠ Season 11; year=2001
		{"Blade Runner 2049 (2017) 1080p", 0, 2017},           // parenthesized year wins over 2049
		{"Blade Runner 2049 1080p x265", 0, 0},                // only a far-future title-number → year unknown
		{"Movie.2019.2020.1080p", 0, 2019},                    // first plausible year, not the max
		{"Show.Season 3.1080p", 3, 0},                         // "Season 3" still parses
		{"Show.S03.1080p.WEB-DL", 3, 0},                       // "S03" still parses
		{"Show.S04E12.720p", 4, 0},                            // S/E still parses (episode via reSE)
	}
	for _, tc := range cases {
		got := Parse(tc.title)
		if got.Season != tc.wantSeason {
			t.Errorf("[%s] season: got %d, want %d", tc.title, got.Season, tc.wantSeason)
		}
		if got.Year != tc.wantYear {
			t.Errorf("[%s] year: got %d, want %d", tc.title, got.Year, tc.wantYear)
		}
	}
}

func assertQualityEqual(t *testing.T, title string, got, want Quality) {
	t.Helper()
	if got.Resolution != want.Resolution {
		t.Errorf("[%s] resolution: got %q, want %q", title, got.Resolution, want.Resolution)
	}
	if got.Codec != want.Codec {
		t.Errorf("[%s] codec: got %q, want %q", title, got.Codec, want.Codec)
	}
	if got.Source != want.Source {
		t.Errorf("[%s] source: got %q, want %q", title, got.Source, want.Source)
	}
	if got.Group != want.Group {
		t.Errorf("[%s] group: got %q, want %q", title, got.Group, want.Group)
	}
	if got.Year != want.Year {
		t.Errorf("[%s] year: got %d, want %d", title, got.Year, want.Year)
	}
	if got.Season != want.Season || got.Episode != want.Episode {
		t.Errorf("[%s] S/E: got S%dE%d, want S%dE%d", title, got.Season, got.Episode, want.Season, want.Episode)
	}
	if got.HDR != want.HDR {
		t.Errorf("[%s] HDR: got %v, want %v", title, got.HDR, want.HDR)
	}
	if got.DolbyVis != want.DolbyVis {
		t.Errorf("[%s] DolbyVis: got %v, want %v", title, got.DolbyVis, want.DolbyVis)
	}
}
