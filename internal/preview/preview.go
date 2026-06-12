// Package preview implements safe, read-only inspection of container files
// (zip/tar/tar.gz/rar archives, CBZ/CBR comics, EPUB books) for the universal
// file viewer. Everything here streams from an abstract byte source — a local
// file or a torrent file reader — and never touches the filesystem for
// extraction, so hostile entry names ("../../etc/cron.d/x") have no effect.
//
// Hard limits guard against decompression bombs: listings are truncated at
// MaxListEntries and any single entry read is capped (the caller picks the cap
// per use case). Nested archives are never recursed into.
package preview

import (
	"errors"
	"path"
	"strings"
)

// Limits. Caps are per-request ceilings on DECOMPRESSED bytes — the zip-bomb
// guard. They're deliberately conservative: previews are for looking, the
// download button exists for everything else.
const (
	// MaxListEntries truncates archive listings (a zip can declare millions of
	// entries in a few KB of central directory).
	MaxListEntries = 2000
	// MaxEntryBytes caps a single inner-entry preview (text or image).
	MaxEntryBytes = 10 << 20 // 10 MiB
	// MaxComicPageBytes caps one comic page image (scans can be large).
	MaxComicPageBytes = 30 << 20 // 30 MiB
	// MaxChapterBytes caps one EPUB chapter document.
	MaxChapterBytes = 2 << 20 // 2 MiB
	// MaxResourceBytes caps one EPUB resource (image/css/font).
	MaxResourceBytes = 10 << 20 // 10 MiB
)

// ErrEntryTooLarge is returned when an entry exceeds the requested cap while
// decompressing — distinguishable from generic I/O errors so handlers can map
// it to 413.
var ErrEntryTooLarge = errors.New("entry exceeds preview size limit")

// ErrEntryNotFound is returned when the requested entry name isn't present.
var ErrEntryNotFound = errors.New("entry not found in archive")

// Entry is one item in an archive listing.
type Entry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Dir  bool   `json:"dir,omitempty"`
}

// SafeEntryName reports whether an archive entry name is safe to surface and
// match: relative, no "..", no NUL, not empty. We never write entries to disk,
// so this is defense in depth (it also keeps hostile names out of listings).
func SafeEntryName(name string) bool {
	if name == "" || strings.ContainsRune(name, 0) {
		return false
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return false
	}
	// Windows drive prefix (C:\evil) — path.Clean won't catch it.
	if len(name) >= 2 && name[1] == ':' {
		return false
	}
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}
	return true
}

// imageExts are the raster formats we serve inline from inside containers.
// SVG is intentionally separate (svgExt): it's XML that can script, so it gets
// a CSP-sandboxed response instead of plain inline.
var imageExts = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".bmp":  "image/bmp",
}

const svgExt = ".svg"

// textExts are extensions previewed as plain text from inside archives.
// Always served as text/plain regardless of what the extension suggests
// (html source included) — content from torrents is hostile by default.
var textExts = map[string]bool{
	".txt": true, ".md": true, ".nfo": true, ".info": true, ".log": true,
	".srt": true, ".vtt": true, ".ass": true, ".ssa": true, ".sub": true,
	".json": true, ".xml": true, ".csv": true, ".cue": true, ".sfv": true,
	".m3u": true, ".m3u8": true, ".yaml": true, ".yml": true, ".ini": true,
	".conf": true, ".cfg": true, ".toml": true, ".html": true, ".htm": true,
	".css": true, ".js": true, ".ts": true, ".py": true, ".go": true,
	".sh": true, ".bat": true, ".rst": true, ".tex": true, ".opf": true,
	".ncx": true, ".xhtml": true, ".diz": true,
}

// EntryContentType classifies an inner entry name for preview serving.
// Returns the Content-Type to use and whether the type is allowed inline.
// Anything not allowed must be refused (the UI offers full-file download of
// the container instead — we don't proxy arbitrary bytes out of archives).
func EntryContentType(name string) (contentType string, ok bool) {
	ext := strings.ToLower(path.Ext(name))
	if ct, isImg := imageExts[ext]; isImg {
		return ct, true
	}
	if ext == svgExt {
		// Caller MUST pair this with a CSP sandbox header (see handlers).
		return "image/svg+xml", true
	}
	if textExts[ext] {
		return "text/plain; charset=utf-8", true
	}
	base := strings.ToLower(path.Base(name))
	if base == "readme" || base == "license" || base == "changelog" {
		return "text/plain; charset=utf-8", true
	}
	return "", false
}

// IsImageEntry reports whether the entry is a raster/vector image we can show.
func IsImageEntry(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	_, raster := imageExts[ext]
	return raster || ext == svgExt
}

// IsComicPage reports whether the entry counts as a comic page (raster only —
// no comic ships SVG pages, and excluding it avoids the scripting headache).
func IsComicPage(name string) bool {
	_, raster := imageExts[strings.ToLower(path.Ext(name))]
	return raster
}
