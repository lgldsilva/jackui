package downloads

import (
	"testing"

	"github.com/luizg/jackui/internal/jackett"
)

func TestSizeWithinTolerance(t *testing.T) {
	cases := []struct {
		a, b int64
		tol  float64
		want bool
	}{
		{1000, 1050, 0.10, true},  // +5%
		{1000, 1100, 0.10, true},  // +10% exact
		{1000, 1101, 0.10, false}, // just over
		{1000, 900, 0.10, true},   // -10%
		{0, 1000, 0.10, false},    // unknown original
		{1000, 0, 0.10, false},    // unknown candidate
	}
	for _, c := range cases {
		if got := sizeWithinTolerance(c.a, c.b, c.tol); got != c.want {
			t.Errorf("sizeWithinTolerance(%d,%d,%v)=%v want %v", c.a, c.b, c.tol, got, c.want)
		}
	}
}

func TestMatchAlternatives_FiltersAndSorts(t *testing.T) {
	orig := Download{InfoHash: "orighash", Name: "The Show S01E02 1080p x265-GRP", FileSize: 1_000_000_000}
	results := []jackett.Result{
		{Title: "The Show S01E02 1080p WEB-DL", InfoHash: "alt1", MagnetURI: "magnet:alt1", Size: 1_050_000_000, Seeders: 10},
		{Title: "The Show S01E02 720p", InfoHash: "alt2", MagnetURI: "magnet:alt2", Size: 980_000_000, Seeders: 50},
		{Title: "The Show S01E03 1080p", InfoHash: "alt3", MagnetURI: "magnet:alt3", Size: 1_010_000_000, Seeders: 99}, // wrong episode
		{Title: "The Show S01E02 1080p", InfoHash: "orighash", MagnetURI: "magnet:orig", Size: 1_000_000_000, Seeders: 80}, // same hash
		{Title: "The Show S01E02 4GB REMUX", InfoHash: "alt4", MagnetURI: "magnet:alt4", Size: 4_000_000_000, Seeders: 100}, // size off
		{Title: "The Show S01E02 no magnet", InfoHash: "alt5", MagnetURI: "", Size: 1_000_000_000, Seeders: 70}, // no magnet
	}
	got := matchAlternatives(orig, results, 5)
	if len(got) != 2 {
		t.Fatalf("expected 2 matches (alt1, alt2), got %d: %+v", len(got), got)
	}
	// Sorted by seeders desc → alt2 (50) before alt1 (10).
	if got[0].InfoHash != "alt2" || got[1].InfoHash != "alt1" {
		t.Errorf("expected [alt2, alt1] by seeders, got [%s, %s]", got[0].InfoHash, got[1].InfoHash)
	}
}

func TestMatchAlternatives_RespectsLimit(t *testing.T) {
	orig := Download{InfoHash: "o", Name: "Movie 2020 1080p", FileSize: 1_000_000_000}
	results := []jackett.Result{
		{Title: "Movie 2020 a", InfoHash: "a", MagnetURI: "m", Size: 1_000_000_000, Seeders: 5},
		{Title: "Movie 2020 b", InfoHash: "b", MagnetURI: "m", Size: 1_000_000_000, Seeders: 4},
		{Title: "Movie 2020 c", InfoHash: "c", MagnetURI: "m", Size: 1_000_000_000, Seeders: 3},
	}
	if got := matchAlternatives(orig, results, 2); len(got) != 2 {
		t.Fatalf("expected limit 2, got %d", len(got))
	}
}

func TestMatchAlternatives_YearMismatch(t *testing.T) {
	orig := Download{InfoHash: "o", Name: "Movie 2020 1080p", FileSize: 1_000_000_000}
	results := []jackett.Result{
		{Title: "Movie 2019 1080p", InfoHash: "a", MagnetURI: "m", Size: 1_000_000_000, Seeders: 5}, // wrong year
		{Title: "Movie 2020 1080p REPACK", InfoHash: "b", MagnetURI: "m", Size: 1_000_000_000, Seeders: 4},
	}
	got := matchAlternatives(orig, results, 5)
	if len(got) != 1 || got[0].InfoHash != "b" {
		t.Fatalf("expected only the 2020 match, got %+v", got)
	}
}

func TestCleanQuery(t *testing.T) {
	cases := map[string]string{
		"The.Show.S01E02.1080p.x265-GRP": "The Show S01E02",
		"Some_Movie_2020_720p_WEB-DL":    "Some Movie 2020",
		"Plain Title":                    "Plain Title",
		"Movie.2020.BluRay.REMUX":        "Movie 2020",
	}
	for in, want := range cases {
		if got := cleanQuery(in); got != want {
			t.Errorf("cleanQuery(%q)=%q want %q", in, got, want)
		}
	}
}
