package watchlist

import (
	"strings"

	"github.com/lgldsilva/jackui/internal/parser"
)

// resolutionRank orders the resolutions the parser can emit. Unknown ("") ranks
// zero so a min-resolution filter rejects releases without a detectable tag —
// auto-download must be conservative: when in doubt, notify instead of fetch.
var resolutionRank = map[string]int{
	"480p":  1,
	"720p":  2,
	"1080p": 3,
	"2160p": 4,
}

// MatchesFilters reports whether a release title/size passes this watchlist's
// auto-download quality filters. Seeders are checked by the caller (the same
// min-seeders gate also guards notifications).
func (w *Watchlist) MatchesFilters(title string, size int64) bool {
	q := parser.Parse(title)
	if w.MinResolution != "" &&
		resolutionRank[q.Resolution] < resolutionRank[strings.ToLower(w.MinResolution)] {
		return false
	}
	if w.Codec != "" && !strings.EqualFold(w.Codec, q.Codec) {
		return false
	}
	if w.MaxSizeBytes > 0 && size > w.MaxSizeBytes {
		return false
	}
	return true
}
