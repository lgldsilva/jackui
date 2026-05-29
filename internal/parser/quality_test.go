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
