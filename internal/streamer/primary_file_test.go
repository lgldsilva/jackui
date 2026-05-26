package streamer

import "testing"

// Real Breaking Bad torrent shape — featurettes are 3GB+, real episodes are
// ~1.3GB each. The old "largest video" heuristic picked the featurette and
// stalled playback forever because the user expected an episode. This test
// pins the new behavior: pick episode S01E01 even though it's smaller.
func TestPickPrimaryFileSeriesIgnoresFeaturettes(t *testing.T) {
	files := []FileInfo{
		{Index: 0, Path: "BB/Featurettes/Season 5/Featurettes 5.1/Behind The Scenes.mkv", Size: 3_500_000_000, IsVideo: true},
		{Index: 1, Path: "BB/Featurettes/Season 1/Inside Breaking Bad.mkv", Size: 235_000_000, IsVideo: true},
		{Index: 2, Path: "BB/Season 1/Breaking.Bad.S01E01.mkv", Size: 1_300_000_000, IsVideo: true},
		{Index: 3, Path: "BB/Season 1/Breaking.Bad.S01E02.mkv", Size: 1_200_000_000, IsVideo: true},
		{Index: 4, Path: "BB/Season 1/Breaking.Bad.S01E03.mkv", Size: 1_250_000_000, IsVideo: true},
		{Index: 5, Path: "BB/Season 2/Breaking.Bad.S02E01.mkv", Size: 1_400_000_000, IsVideo: true},
	}
	got := pickPrimaryFile(files)
	if got != 2 {
		t.Errorf("expected S01E01 (index 2), got index %d (%s)", got, files[got].Path)
	}
}

// Movie torrent: single huge mkv + a smaller "sample" file. Should pick the
// real movie, never the sample.
func TestPickPrimaryFileMovieIgnoresSample(t *testing.T) {
	files := []FileInfo{
		{Index: 0, Path: "Inception (2010) [1080p]/sample.mkv", Size: 50_000_000, IsVideo: true},
		{Index: 1, Path: "Inception (2010) [1080p]/Inception.2010.1080p.BluRay.x264.mkv", Size: 14_000_000_000, IsVideo: true},
		{Index: 2, Path: "Inception (2010) [1080p]/poster.jpg", Size: 500_000, IsVideo: false},
	}
	got := pickPrimaryFile(files)
	if got != 1 {
		t.Errorf("expected the main movie (index 1), got %d (%s)", got, files[got].Path)
	}
}

// Series with non-standard naming (no S?E? tags) + extras: fall back to largest
// non-extra video.
func TestPickPrimaryFileFallbackToLargest(t *testing.T) {
	files := []FileInfo{
		{Index: 0, Path: "Show/Bonus/Extras.mkv", Size: 5_000_000_000, IsVideo: true},
		{Index: 1, Path: "Show/Episode 1.mkv", Size: 800_000_000, IsVideo: true},
		{Index: 2, Path: "Show/Episode 2.mkv", Size: 900_000_000, IsVideo: true},
	}
	got := pickPrimaryFile(files)
	// Only 2 unique "episodes" (less than the 3-threshold for series detection),
	// so we fall back to largest non-extra video — Episode 2.
	if got != 2 {
		t.Errorf("expected Episode 2 (index 2, largest non-extra), got %d (%s)", got, files[got].Path)
	}
}

// All files are extras (rare but possible — extras-only torrent). The
// last-resort branch should still return *some* video rather than -1.
func TestPickPrimaryFileExtrasOnlyFallsBackToFirstVideo(t *testing.T) {
	files := []FileInfo{
		{Index: 0, Path: "Show/Featurette.mkv", Size: 500_000_000, IsVideo: true},
		{Index: 1, Path: "Show/Sample.mkv", Size: 50_000_000, IsVideo: true},
		{Index: 2, Path: "Show/README.txt", Size: 1000, IsVideo: false},
	}
	got := pickPrimaryFile(files)
	if got != 0 {
		t.Errorf("expected first video as last resort (index 0), got %d", got)
	}
}

// Audio-only torrent (no video files). Should return -1 — the player will then
// pick audio file 0 client-side.
func TestPickPrimaryFileAudioOnlyReturnsMinusOne(t *testing.T) {
	files := []FileInfo{
		{Index: 0, Path: "Album/01 Track One.flac", Size: 30_000_000, IsVideo: false},
		{Index: 1, Path: "Album/02 Track Two.flac", Size: 35_000_000, IsVideo: false},
	}
	got := pickPrimaryFile(files)
	if got != -1 {
		t.Errorf("expected -1 for audio-only torrent, got %d", got)
	}
}
