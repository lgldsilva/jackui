package parser

import "testing"

func TestDetectKind(t *testing.T) {
	cases := []struct {
		title      string
		categoryID int
		want       MediaKind
	}{
		// Extension wins over category
		{"song.flac", 5000, KindAudio},
		{"movie.mkv", 3000, KindVideo},
		// Category when extension is absent
		{"Album Name 2024", 3010, KindAudio},
		{"Show S01E02", 5010, KindVideo},
		// Textual hint
		{"Artist - Album FLAC", 0, KindAudio},
		{"Movie 1080p WEBRip", 0, KindVideo},
		// Default falls back to video
		{"Random.thing", 9999, KindVideo},
	}
	for _, tc := range cases {
		got := DetectKind(tc.title, tc.categoryID)
		if got != tc.want {
			t.Errorf("DetectKind(%q, %d) = %q, want %q", tc.title, tc.categoryID, got, tc.want)
		}
	}
}

func TestIsPlayable(t *testing.T) {
	const magnet = "magnet:?xt=urn:btih:abc"
	cases := []struct {
		name       string
		title      string
		categoryID int
		magnet     string
		resolution string
		want       bool
	}{
		{"no magnet", "Movie 1080p", 5000, "", "1080p", false},
		{"ebook PDF rejected", "Book.pdf", 0, magnet, "", false},
		{"zip rejected", "Pack.zip", 0, magnet, "", false},
		{"ebook tag rejected", "Foo Bar Ebook", 0, magnet, "", false},
		{"movie category accepted", "Whatever", 2000, magnet, "", true},
		{"resolution accepted", "Foo 2024", 9999, magnet, "1080p", true},
		{"video extension accepted", "Foo.mkv", 9999, magnet, "", true},
		{"audio hint accepted", "Artist FLAC Discography", 9999, magnet, "", true},
		{"unknown falls back to true", "Just a title", 9999, magnet, "", true},
	}
	for _, tc := range cases {
		got := IsPlayable(tc.title, tc.categoryID, tc.magnet, tc.resolution)
		if got != tc.want {
			t.Errorf("[%s] IsPlayable(%q, %d) = %v, want %v",
				tc.name, tc.title, tc.categoryID, got, tc.want)
		}
	}
}
