package handlers

import (
	"encoding/base64"
	"encoding/json"
	"log"
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

// hiddenCurtain returns the identity set (info_hashes + normalized names) of
// favourites in a hidden folder that should therefore be dropped from default
// listings (Continue Watching, downloads, the active swarm view). Returns the
// zero curtain — meaning "filter nothing" — when the request opened the curtain
// (easter egg → X-JackUI-Reveal-Hidden) or favourites is unavailable. A DB
// error also fails open (the listing query hits the same database, so a real
// outage fails the endpoint anyway) but is LOGGED: the old silent swallow made
// a curtain failure indistinguishable from "nothing is hidden".
func hiddenCurtain(c *gin.Context, s *streamer.Streamer, userID int, includeAll bool) streamer.HiddenCurtain {
	if middleware.IsRevealHidden(c) || s == nil || s.Favorites() == nil {
		return streamer.HiddenCurtain{}
	}
	curtain, err := s.Favorites().HiddenFavorites(userID, includeAll)
	if err != nil {
		log.Printf("hidden curtain: favourites query failed (failing open): %v", err)
		return streamer.HiddenCurtain{}
	}
	return curtain
}

// curtainHidden reports whether an (info_hash, name) pair sits behind the
// curtain: a hash match OR a name match hides. Both sides are normalized
// (trim + lowercase) so a favourite stored with an empty or unusually-cased
// hash still hides its library/download row by title — hiding is a per-title
// decision, not a per-row one.
func curtainHidden(infoHash, name string, curtain streamer.HiddenCurtain) bool {
	if curtain.Hashes[strings.ToLower(strings.TrimSpace(infoHash))] {
		return true
	}
	return curtain.Names[strings.ToLower(strings.TrimSpace(name))]
}

// hiddenDownloadFilter aggregates every "hidden curtain" source the downloads
// list must honour. A download is hidden when EITHER it matches the favourite
// identity curtain by info_hash OR normalized name (also covers the library /
// Continue Watching, which reuses the same matching), OR its on-disk file lives
// under a path the user hid in the local browser (hidden_local_paths, keyed by
// mount+path → resolved here to an absolute prefix). Both are unioned so a
// download leaks through neither curtain.
type hiddenDownloadFilter struct {
	curtain  streamer.HiddenCurtain // identity curtain: favourite hashes + names
	prefixes []string               // absolute on-disk path prefixes (hidden local paths)
}

// empty reports whether the filter would drop nothing (so callers can skip the
// per-row work entirely).
func (h hiddenDownloadFilter) empty() bool {
	return h.curtain.Empty() && len(h.prefixes) == 0
}

// hides reports whether the given download should be dropped from the listing.
func (h hiddenDownloadFilter) hides(d downloads.Download) bool {
	if curtainHidden(d.InfoHash, d.Name, h.curtain) {
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
	if curtain, err := s.Favorites().HiddenFavorites(userID, includeAll); err == nil {
		f.curtain = curtain
	} else {
		log.Printf("hidden curtain: favourites query failed (failing open): %v", err)
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

// dropHiddenLibrary removes library (Continue Watching) entries hidden by the
// identity curtain: an entry is dropped when its info_hash OR its normalized
// name matches. The name fallback keeps hash-less hidden favourites effective —
// a quick-favourite whose hash never resolved must still hide its Continue
// Watching row. An empty curtain returns the list untouched.
func dropHiddenLibrary(list []library.Entry, curtain streamer.HiddenCurtain) []library.Entry {
	if curtain.Empty() {
		return list
	}
	out := list[:0]
	for _, e := range list {
		if curtainHidden(e.InfoHash, e.Name, curtain) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// dropHiddenLocalLibrary drops Continue Watching rows for local files whose
// (mount, path) sits under a path the user hid in the local browser. Library
// stores local plays as `local-<base64url(json{mount,path})>` info hashes —
// the favourite-folder curtain never matches those by hash, so without this
// pass a hidden local folder still leaked into the home "Continue Watching"
// strip.
func dropHiddenLocalLibrary(c *gin.Context, s *streamer.Streamer, list []library.Entry, userID int) []library.Entry {
	if middleware.IsRevealHidden(c) || s == nil || s.Favorites() == nil || len(list) == 0 {
		return list
	}
	paths, err := s.Favorites().HiddenLocalPaths(userID)
	if err != nil || len(paths) == 0 {
		return list
	}
	// mount → set of hidden relative paths
	byMount := map[string]map[string]bool{}
	for _, p := range paths {
		if byMount[p.Mount] == nil {
			byMount[p.Mount] = map[string]bool{}
		}
		byMount[p.Mount][p.Path] = true
	}
	out := list[:0]
	for _, e := range list {
		if localLibraryEntryHidden(e.InfoHash, byMount) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// localLibraryEntryHidden reports whether a library infoHash encodes a local
// mount+path under a hidden folder. Non-local hashes are never hidden here.
func localLibraryEntryHidden(infoHash string, byMount map[string]map[string]bool) bool {
	mount, path, ok := parseLocalInfoHash(infoHash)
	if !ok {
		return false
	}
	set := byMount[mount]
	if len(set) == 0 {
		return false
	}
	return localPathIsHidden(path, set)
}

// parseLocalInfoHash decodes the frontend/backend `local-<base64url(json)>`
// pseudo-hash. Returns ok=false for torrent hashes or corrupt payloads.
func parseLocalInfoHash(infoHash string) (mount, path string, ok bool) {
	const prefix = "local-"
	if !strings.HasPrefix(infoHash, prefix) {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(infoHash, prefix))
	if err != nil {
		return "", "", false
	}
	var payload struct {
		Mount string `json:"mount"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Mount == "" || payload.Path == "" {
		return "", "", false
	}
	return payload.Mount, payload.Path, true
}

// localPathIsHidden mirrors handlers/local.localPathHidden (ancestor walk) so
// this package does not import the local handler package (import cycle risk).
func localPathIsHidden(path string, hidden map[string]bool) bool {
	if len(hidden) == 0 {
		return false
	}
	p := strings.Trim(path, "/")
	for p != "" {
		if hidden[p] {
			return true
		}
		i := strings.LastIndex(p, "/")
		if i < 0 {
			break
		}
		p = p[:i]
	}
	return false
}
