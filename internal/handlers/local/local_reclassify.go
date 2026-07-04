package local

import (
	"path/filepath"
	"strings"

	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/renamer"
)

// scopedOverrides translates the request's Overrides map (keyed by the un-scoped
// source path the UI sent) into one keyed by the SCOPED source path that
// resolveLocalPaths/promoteOnePath operate on, for the given user. Mirrors
// resolveLocalPaths' scoping so a UserSubpath mount lines the override up with
// the file it edits. Returns nil when there's nothing to apply.
func scopedOverrides(b *lb.Browser, req *localPromoteReq, username string) map[string]string {
	if len(req.Overrides) == 0 {
		return nil
	}
	out := make(map[string]string, len(req.Overrides))
	for src, target := range req.Overrides {
		if strings.TrimSpace(target) == "" {
			continue
		}
		out[b.UserScopedPath(req.Mount, src, username)] = target
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sanitizeOverrideTarget validates and cleans a user-edited destination path
// (relative to the promote base) so it can NEVER escape the base or carry
// filesystem-unsafe characters — the SAME guarantee the AI-computed path has.
// Rejects absolute paths and any ".." segment; runs renamer.SanitizeFilename on
// every segment; and reuses an existing top-level category folder
// case-insensitively (so a typed "movies" lands in the library's "Movies").
// Returns (cleanRelPath, true) on success, ("", false) when the override is
// empty/invalid (the caller then falls back to the AI suggestion).
func sanitizeOverrideTarget(raw string, lc *renamer.LocalContext) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || filepath.IsAbs(raw) {
		return "", false
	}
	// Normalize separators a hand-typed path might use, then sanitize per-segment.
	clean, ok := sanitizeOverrideSegments(strings.ReplaceAll(raw, "\\", "/"))
	if !ok {
		return "", false
	}
	// Reuse an existing destination category folder for the TOP-LEVEL segment
	// (case-insensitive) when the typed name matches one — same taxonomy reuse
	// the AI path gets, so we don't fork "Movies" into a new "movies".
	if lc != nil && len(lc.DestFolders) > 0 && len(clean) > 1 {
		if existing := renamer.ReuseExistingFolder(clean[0], lc.DestFolders); existing != "" {
			clean[0] = existing
		}
	}
	return filepath.Join(clean...), true
}

// sanitizeOverrideSegments splits a "/"-joined path, drops empty/"."-segments,
// rejects any ".." (base escape), and scrubs each remaining segment with
// renamer.SanitizeFilename. Returns ok=false when nothing valid survives.
func sanitizeOverrideSegments(path string) ([]string, bool) {
	segs := strings.Split(path, "/")
	clean := make([]string, 0, len(segs))
	for _, seg := range segs {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." {
			continue // collapse empty/current-dir segments ("a//b", "./a")
		}
		if seg == ".." {
			return nil, false // never allow climbing out of base
		}
		s := renamer.SanitizeFilename(seg)
		if s == "" {
			return nil, false // a segment that sanitizes to nothing is invalid
		}
		clean = append(clean, s)
	}
	if len(clean) == 0 {
		return nil, false
	}
	return clean, true
}
