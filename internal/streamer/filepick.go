package streamer

import (
	"regexp"
	"strconv"
)

var seriesEpisodeRe = regexp.MustCompile(`(?i)s(\d{1,2})e(\d{1,3})`)

// extraTagsRe matches things that look like Featurettes / Extras / Sample / Trailer
// — files we should NEVER pick as primary on a series torrent.
var extraTagsRe = regexp.MustCompile(`(?i)\b(featurette|extras?|bonus|behind[\s\-]?the[\s\-]?scenes|deleted[\s\-]?scenes|making[\s\-]?of|sample|trailer|interview|gag[\s\-]?reel|outtake)s?\b`)

// pickPrimaryFile chooses the file to auto-select when the user "Plays" a
// torrent without specifying one. Picks "the most likely main content":
//
//  1. If 3+ files contain an S?E? pattern AND aren't tagged as extras, pick
//     the lowest (season, episode) episode — the natural starting point for
//     a series pack. This handles Breaking Bad-style torrents that ship with
//     huge Featurettes that would dwarf a real episode by size.
//  2. Else pick the largest video that isn't tagged as an extra — covers
//     single-movie torrents and series with non-standard naming.
//  3. Fall back to the first video, or -1 if none.
func pickPrimaryFile(files []FileInfo) int {
	if idx, ok := pickEpisodeStart(files); ok {
		return idx
	}
	if idx, ok := pickLargestNonExtra(files); ok {
		return idx
	}
	return firstVideoIndex(files)
}

func nonExtraVideos(files []FileInfo) []FileInfo {
	var out []FileInfo
	for _, f := range files {
		if f.IsVideo && !extraTagsRe.MatchString(f.Path) {
			out = append(out, f)
		}
	}
	return out
}

func pickEpisodeStart(files []FileInfo) (int, bool) {
	type epHit struct{ idx, season, episode int }
	var episodes []epHit
	for _, f := range nonExtraVideos(files) {
		m := seriesEpisodeRe.FindStringSubmatch(f.Path)
		if m == nil {
			continue
		}
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		episodes = append(episodes, epHit{idx: f.Index, season: s, episode: e})
	}
	if len(episodes) < 3 {
		return 0, false
	}
	best := episodes[0]
	for _, ep := range episodes[1:] {
		if ep.season < best.season || (ep.season == best.season && ep.episode < best.episode) {
			best = ep
		}
	}
	return best.idx, true
}

func pickLargestNonExtra(files []FileInfo) (int, bool) {
	largestIdx, largestSize := -1, int64(0)
	for _, f := range nonExtraVideos(files) {
		if f.Size > largestSize {
			largestIdx, largestSize = f.Index, f.Size
		}
	}
	if largestIdx >= 0 {
		return largestIdx, true
	}
	return 0, false
}

func firstVideoIndex(files []FileInfo) int {
	for _, f := range files {
		if f.IsVideo {
			return f.Index
		}
	}
	return -1
}
