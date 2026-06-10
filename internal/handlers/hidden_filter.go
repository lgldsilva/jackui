package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// hiddenHashSet returns the info_hashes that belong to a hidden favourite folder
// and should therefore be dropped from default listings (Continue Watching,
// downloads). Returns nil — meaning "filter nothing" — when the request opened
// the curtain (easter egg → X-JackUI-Reveal-Hidden), favourites is unavailable,
// or there are no hidden hashes. The caller treats nil as a no-op.
func hiddenHashSet(c *gin.Context, s *streamer.Streamer, userID int, includeAll bool) map[string]bool {
	if middleware.IsRevealHidden(c) || s == nil || s.Favorites() == nil {
		return nil
	}
	set, err := s.Favorites().HiddenHashSet(userID, includeAll)
	if err != nil || len(set) == 0 {
		return nil
	}
	return set
}

// dropHiddenDownloads removes downloads whose info_hash is in the hidden set.
// A nil/empty set returns the list untouched.
func dropHiddenDownloads(list []downloads.Download, hidden map[string]bool) []downloads.Download {
	if len(hidden) == 0 {
		return list
	}
	out := list[:0]
	for _, d := range list {
		if !hidden[d.InfoHash] {
			out = append(out, d)
		}
	}
	return out
}

// dropHiddenLibrary removes library (Continue Watching) entries whose info_hash
// is in the hidden set. A nil/empty set returns the list untouched.
func dropHiddenLibrary(list []library.Entry, hidden map[string]bool) []library.Entry {
	if len(hidden) == 0 {
		return list
	}
	out := list[:0]
	for _, e := range list {
		if !hidden[e.InfoHash] {
			out = append(out, e)
		}
	}
	return out
}
