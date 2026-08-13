package handlers

import "github.com/lgldsilva/jackui/internal/streamer"

// pickKnownDuration returns a duration hint the HLS session can use to skip
// the blocking 30s seekable probe. The player already probed to decide
// direct-play vs HLS, so this is almost always a cache hit. 0 means "unknown
// — fall back to the in-session probe".
func pickKnownDuration(pr streamer.ProbeResult, ok bool) float64 {
	if !ok || pr.DurationSec <= 0 {
		return 0
	}
	return pr.DurationSec
}
