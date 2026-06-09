package downloads

import (
	"sort"

	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/parser"
)

// sizeTolerance is how far an alternative's size may differ from the original
// and still be considered the same content (the strongest match signal).
const sizeTolerance = 0.10

// matchAlternatives filters Jackett results down to those likely to be the SAME
// content as the original download, for source rotation:
//   - a usable magnet + info_hash (can't rotate to a hashless/.torrent-only result)
//   - a DIFFERENT info_hash than the original (a real alternative, not the same torrent)
//   - size within sizeTolerance of the original (strongest signal)
//   - for episodic content, the same season+episode; matching year when both known
//
// Returns up to `limit` matches, highest seeders first. Pure/testable.
func matchAlternatives(orig Download, results []jackett.Result, limit int) []jackett.Result {
	oq := parser.Parse(orig.Name)
	matched := make([]jackett.Result, 0, len(results))
	for _, r := range results {
		if isAlternativeMatch(orig, oq, r) {
			matched = append(matched, r)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool { return matched[i].Seeders > matched[j].Seeders })
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return matched
}

func isAlternativeMatch(orig Download, oq parser.Quality, r jackett.Result) bool {
	if r.MagnetURI == "" || r.InfoHash == "" || r.InfoHash == orig.InfoHash {
		return false
	}
	if !sizeWithinTolerance(orig.FileSize, r.Size, sizeTolerance) {
		return false
	}
	rq := parser.Parse(r.Title)
	if oq.Season != rq.Season || oq.Episode != rq.Episode {
		return false
	}
	if oq.Year != 0 && rq.Year != 0 && oq.Year != rq.Year {
		return false
	}
	return true
}

// sizeWithinTolerance reports whether b is within tol (fraction) of a. Unknown
// sizes (<=0) never match — we won't risk rotating to a different release.
func sizeWithinTolerance(a, b int64, tol float64) bool {
	if a <= 0 || b <= 0 {
		return false
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return float64(diff) <= float64(a)*tol
}
