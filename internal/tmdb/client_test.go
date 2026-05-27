package tmdb

import "testing"

func TestCleanQuery(t *testing.T) {
	cases := []struct {
		raw       string
		wantTitle string
		wantYear  int
	}{
		{"Inception.2010.1080p.BluRay.x264-SPARKS", "Inception", 2010},
		{"The.Matrix.1999.2160p.UHD.BluRay.x265-TERMINAL", "The Matrix", 1999},
		{"Breaking.Bad.S03E07.720p.HDTV.x264-CTU", "Breaking Bad", 0},
		{"Dune Part Two (2024) [1080p] [WEBRip]", "Dune Part Two", 2024},
		{"Some Movie DUBLADO 1080p", "Some Movie", 0},
	}
	for _, tc := range cases {
		title, year := cleanQuery(tc.raw)
		if title != tc.wantTitle {
			t.Errorf("cleanQuery(%q) title = %q, want %q", tc.raw, title, tc.wantTitle)
		}
		if year != tc.wantYear {
			t.Errorf("cleanQuery(%q) year = %d, want %d", tc.raw, year, tc.wantYear)
		}
	}
}

func TestCleanQueryEmptyForJunkOnly(t *testing.T) {
	// A name that's nothing but release tags should clean to empty so Match
	// short-circuits instead of searching TMDB for "1080p x264".
	title, _ := cleanQuery("1080p.x264.BluRay")
	if title != "" {
		t.Errorf("expected empty title for tag-only input, got %q", title)
	}
}
