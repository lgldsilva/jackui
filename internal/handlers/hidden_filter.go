package handlers

import (
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/local"
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

// hiddenDownloadFilter aggregates every "hidden curtain" source the downloads
// list must honour. A download is hidden when EITHER its info_hash is in a
// hidden favourite folder (also covers the library / Continue Watching, which
// reuses the favourite hashes), OR its on-disk file lives under a path the user
// hid in the local browser (hidden_local_paths, keyed by mount+path → resolved
// here to an absolute prefix). Both are unioned so a download leaks through
// neither curtain.
type hiddenDownloadFilter struct {
	hashes   map[string]bool // info_hash → hidden (favourites + library)
	prefixes []string        // absolute on-disk path prefixes (hidden local paths)
}

// empty reports whether the filter would drop nothing (so callers can skip the
// per-row work entirely).
func (h hiddenDownloadFilter) empty() bool {
	return len(h.hashes) == 0 && len(h.prefixes) == 0
}

// hides reports whether the given download should be dropped from the listing.
func (h hiddenDownloadFilter) hides(d downloads.Download) bool {
	if d.InfoHash != "" && h.hashes[d.InfoHash] {
		return true
	}
	if d.FilePath == "" {
		return false
	}
	for _, p := range h.prefixes {
		if pathUnder(d.FilePath, p) {
			return true
		}
	}
	return false
}

// pathUnder reports whether filePath is prefix itself or sits beneath it. The
// trailing-separator guard avoids "/a/movies" matching "/a/movies-extra"
// (same pattern as downloads.Store path-prefix matching and markPromoted).
func pathUnder(filePath, prefix string) bool {
	if prefix == "" {
		return false
	}
	return filePath == prefix || strings.HasPrefix(filePath, prefix+string(os.PathSeparator))
}

// buildHiddenDownloadFilter assembles the hidden filter from all sources,
// scoped to the requesting user (includeAll=true spans every user, for the
// admin "all downloads" view). Returns an empty filter (no-op) when the request
// opened the curtain (X-JackUI-Reveal-Hidden) or favourites is unavailable.
func buildHiddenDownloadFilter(c *gin.Context, s *streamer.Streamer, b *local.Browser, authStore *auth.Store, userID int, includeAll bool) hiddenDownloadFilter {
	if middleware.IsRevealHidden(c) || s == nil || s.Favorites() == nil {
		return hiddenDownloadFilter{}
	}
	f := hiddenDownloadFilter{}
	if set, err := s.Favorites().HiddenHashSet(userID, includeAll); err == nil {
		f.hashes = set
	}
	f.prefixes = hiddenLocalPrefixes(s, b, authStore, userID, includeAll)
	return f
}

// hiddenLocalPrefixes resolves the user's (or, for includeAll, every user's)
// hidden local (mount, path) entries to absolute on-disk path prefixes, so a
// download whose file lives under a hidden local folder can be matched by path.
// Resolution honours UserSubpath mounts by resolving under the OWNER's scope.
func hiddenLocalPrefixes(s *streamer.Streamer, b *local.Browser, authStore *auth.Store, userID int, includeAll bool) []string {
	if b == nil {
		return nil
	}
	owned := collectHiddenLocalPaths(s, userID, includeAll)
	if len(owned) == 0 {
		return nil
	}
	uc := userCache{}
	out := make([]string, 0, len(owned))
	for _, hp := range owned {
		username := uc.get(authStore, hp.UserID)
		abs, err := b.ResolvePathFor(hp.Mount, hp.Path, username)
		if err != nil || abs == "" {
			continue
		}
		out = append(out, abs)
	}
	return out
}

// collectHiddenLocalPaths fetches the hidden local paths for a single user, or
// for everyone when includeAll is set (admin view).
func collectHiddenLocalPaths(s *streamer.Streamer, userID int, includeAll bool) []streamer.HiddenLocalPathOwned {
	if includeAll {
		all, err := s.Favorites().HiddenLocalPathsAll()
		if err != nil {
			return nil
		}
		return all
	}
	paths, err := s.Favorites().HiddenLocalPaths(userID)
	if err != nil {
		return nil
	}
	owned := make([]streamer.HiddenLocalPathOwned, 0, len(paths))
	for _, p := range paths {
		owned = append(owned, streamer.HiddenLocalPathOwned{UserID: userID, Mount: p.Mount, Path: p.Path})
	}
	return owned
}

// dropHiddenDownloads removes downloads hidden by any curtain source (favourite
// hidden folder by info_hash, or local hidden path by file location). An empty
// filter returns the list untouched.
func dropHiddenDownloads(list []downloads.Download, filter hiddenDownloadFilter) []downloads.Download {
	if filter.empty() {
		return list
	}
	out := list[:0]
	for _, d := range list {
		if !filter.hides(d) {
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
