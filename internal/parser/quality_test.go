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
		got := Parse(tc.title)
		if got.Resolution != tc.want.Resolution {
			t.Errorf("[%s] resolution: got %q, want %q", tc.title, got.Resolution, tc.want.Resolution)
		}
		if got.Codec != tc.want.Codec {
			t.Errorf("[%s] codec: got %q, want %q", tc.title, got.Codec, tc.want.Codec)
		}
		if got.Source != tc.want.Source {
			t.Errorf("[%s] source: got %q, want %q", tc.title, got.Source, tc.want.Source)
		}
		if got.Group != tc.want.Group {
			t.Errorf("[%s] group: got %q, want %q", tc.title, got.Group, tc.want.Group)
		}
		if got.Year != tc.want.Year {
			t.Errorf("[%s] year: got %d, want %d", tc.title, got.Year, tc.want.Year)
		}
		if got.Season != tc.want.Season || got.Episode != tc.want.Episode {
			t.Errorf("[%s] S/E: got S%dE%d, want S%dE%d", tc.title, got.Season, got.Episode, tc.want.Season, tc.want.Episode)
		}
		if got.HDR != tc.want.HDR {
			t.Errorf("[%s] HDR: got %v, want %v", tc.title, got.HDR, tc.want.HDR)
		}
		if got.DolbyVis != tc.want.DolbyVis {
			t.Errorf("[%s] DolbyVis: got %v, want %v", tc.title, got.DolbyVis, tc.want.DolbyVis)
		}
	}
}
