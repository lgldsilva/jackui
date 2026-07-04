package downloads

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/anacrolix/torrent"
)

// Completion: destino/paths/naming dos downloads concluídos — extraído de worker.go.
// wholeTorrentRelPaths returns the content files' torrent-relative paths, skipping
// BEP 47 pad files (attr "p") — piece-alignment filler, never materialized.
func wholeTorrentRelPaths(files []*torrent.File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if strings.Contains(f.FileInfo().Attr, "p") {
			continue
		}
		out = append(out, f.Path())
	}
	return out
}

// peerCount is nil-safe len(t.PeerConns()) for diagnostic logs (tests build
// trackedDLs without a live torrent).
func peerCount(t *torrent.Torrent) int {
	if t == nil {
		return 0
	}
	return len(t.PeerConns())
}

// partSuffix is what the anacrolix file storage appends to a file until all its
// pieces verify; a restart mid-verification can leave a *complete* .part behind.
const partSuffix = ".part"

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// resolveCompletedSrc locates the finished file in dataDir for a torrent-relative
// path: the final name, or the leftover ".part" the storage hasn't renamed yet.
// Returns "" when neither exists.
func resolveCompletedSrc(dataDir, relPath string) string {
	src := filepath.Join(dataDir, relPath)
	if fileExists(src) {
		return src
	}
	if part := src + partSuffix; fileExists(part) {
		return part
	}
	return ""
}

// completedDestDir builds the per-user, per-torrent destination directory.
func completedDestDir(downloadDir, username, torrentName string) string {
	dir := downloadDir
	if username != "" {
		dir = filepath.Join(dir, username)
	}
	return filepath.Join(dir, sanitizeFolderName(torrentName))
}

// PromoteDir returns the Transmission-style "completed downloads" directory for
// an *arr download: sharedDir/<sanitized category> (or just sharedDir when the
// category is empty). Shared by the worker (where the finished files land) and
// the Transmission RPC (the download-dir reported back to the *arr) so the two
// always agree on the path the *arr will import from.
func PromoteDir(sharedDir, category string) string {
	if cat := sanitizeFolderName(category); category != "" && cat != "download" {
		return filepath.Join(sharedDir, cat)
	}
	return sharedDir
}

// dirHasFiles reports whether dir exists and contains at least one entry.
func dirHasFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// isOrphanedCompletion reports whether a completed download lost its moved file
// (file_path gone) while its source still sits in the cache — the fingerprint of
// a move interrupted by a restart. Such rows are re-queued on boot so the worker
// re-moves them; the cache-source guard prevents mass re-download when a
// downloadDir is merely unmounted for a moment.
func isOrphanedCompletion(d Download, dataDir string) bool {
	return d.FilePath != "" && !fileExists(d.FilePath) && dirHasFiles(filepath.Join(dataDir, d.Name))
}

// completionDest returns the per-torrent destination directory for a finished
// download. Normally downloadDir[/username]/<torrent>; but an *arr download
// (Source==SourceArr) with auto-promote enabled (and SharedDir set) goes straight
// into sharedDir/<category>/<torrent> — the Transmission-style "completed
// downloads" tree the *arr import from, catalogued by the category the *arr sent
// (no per-user subdir, matching Transmission). Returns "" when no destination is
// configured (legacy: keep the file in DataDir).
func (w *Worker) completionDest(d Download, torrentName string) string {
	base := w.completionBaseDir(d)
	if base == "" {
		return ""
	}
	return filepath.Join(base, sanitizeFolderName(torrentName))
}

// completionBaseDir returns the PARENT directory a finished download lands in,
// WITHOUT the per-torrent name segment. The torrent name isn't known until
// metadata arrives, so the download-to-bulk storage needs just the parent (it
// appends <sanitize(name)> itself inside its TorrentDirMaker). Sharing this with
// completionDest — which appends the SAME <sanitize(name)> — guarantees the
// storage writes exactly where the move expects, making the move a no-op.
// Returns "" when no destination is configured (legacy: keep in DataDir).
func (w *Worker) completionBaseDir(d Download) string {
	// A destination the user explicitly picked (#16) wins over the defaults. It was
	// validated against the user's allowed destinations at create time; the subdir
	// is re-cleaned defensively against traversal here.
	if d.DestBase != "" {
		if sub := cleanDestSubdir(d.DestSubdir); sub != "" {
			return filepath.Join(d.DestBase, sub)
		}
		return d.DestBase
	}
	if w.sharedDir != "" && d.Source == SourceArr && w.queueSettings().AutoPromoteArr {
		return PromoteDir(w.sharedDir, d.Category)
	}
	if w.downloadDir == "" {
		return ""
	}
	base := w.downloadDir
	if w.resolveUsername != nil {
		// Per-user mode: ALWAYS land in a user subdir, never the bare downloadDir.
		// When downloadDir is also a UserSubpath mount root, the bare root is exactly
		// where the boot-time migration scans — a download there gets relocated into
		// <user>/ and, racing the live torrent, duplicated as "name (1)", "name (2)".
		// A transient resolveUsername failure must not drop us there: fall back to a
		// stable user subdir (fallbackUser, typically the admin username — a known
		// user the migration leaves alone).
		u := w.resolveUsername(d.UserID)
		if u == "" {
			u = w.fallbackUser
		}
		if u != "" {
			base = filepath.Join(base, u)
		}
	}
	// Group downloads by their category folder (e.g. .../<user>/Movies/<torrent>) so
	// the browser isn't a flat dump — the torrent's category is the natural grouping.
	// Only when the user didn't pick an explicit destination (handled above).
	if cat := categoryFolder(d.Category); cat != "" {
		base = filepath.Join(base, cat)
	}
	return base
}

// categoryFolder turns a download's category label into a folder segment for
// grouping, or "" when there's nothing useful to group by. Takes the top-level
// label (before any "/" subcategory), drops the "all" placeholder and bare
// numeric torznab IDs (a folder named "5000" helps no one), and sanitizes the
// rest for the filesystem.
func categoryFolder(category string) string {
	c := strings.TrimSpace(category)
	if c == "" || strings.EqualFold(c, "all") {
		return ""
	}
	if i := strings.IndexAny(c, "/\\"); i >= 0 {
		c = strings.TrimSpace(c[:i])
	}
	if c == "" || isAllDigits(c) {
		return ""
	}
	return sanitizeFolderName(c)
}

// isAllDigits reports whether s is non-empty and every rune is an ASCII digit.
func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// cleanDestSubdir defensively sanitizes a stored destination subdir: rejects
// absolute paths and any ".." traversal, returning a cleaned relative path or ""
// (which means "no subdir"). The subdir is already validated at create time;
// this is belt-and-suspenders against a tampered DB row.
func cleanDestSubdir(sub string) string {
	if sub == "" || filepath.IsAbs(sub) {
		return ""
	}
	clean := filepath.Clean(filepath.FromSlash(sub))
	if clean == "." || !filepath.IsLocal(clean) {
		return ""
	}
	return clean
}

// initFilePath computes the row's file_path + size at init time. For
// download-to-bulk it points into the bulk destination (where the storage writes
// the data) so streaming-of-in-progress and the post-completion finalize both
// resolve there; with no destination configured it's the legacy cache path under
// DataDir. Whole-torrent → the per-torrent dir; single/selected file → that dir
// plus the file's tree path (name root stripped, matching the storage layout).
func (w *Worker) initFilePath(d Download, t *torrent.Torrent, f *torrent.File, name string) (string, int64) {
	if base := w.completionBaseDir(d); base != "" {
		dir := filepath.Join(base, sanitizeFolderName(name)) // == completionDest(d, name)
		if f != nil {
			return filepath.Join(dir, bulkRel(name, f)), f.Length()
		}
		return dir, t.Length()
	}
	if f != nil {
		return filepath.Join(w.dataDir, f.Path()), f.Length()
	}
	return filepath.Join(w.dataDir, name), t.Length()
}

// bulkRel returns a torrent file's path relative to its per-torrent dir, matching
// the download storage layout: the internal tree WITHOUT the torrent-name root
// (single-file torrents have no root, so it's just the file name).
func bulkRel(name string, f *torrent.File) string {
	return filepath.FromSlash(strings.TrimPrefix(f.Path(), name+"/"))
}

// sanitizeFolderName turns a torrent name into ONE safe path segment for the
// per-torrent destination folder: strips path separators and traversal, drops
// control chars, trims trailing dots/spaces, and caps the length. Never returns
// "", ".", or ".." (which would escape or no-op the join) — falls back to "download".
func sanitizeFolderName(name string) string {
	// Neutralize path separators (a single segment can't traverse without them);
	// `.`/`..` are handled by the trailing-trim + the final guard below.
	name = strings.NewReplacer("/", "_", "\\", "_").Replace(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if len(name) > 200 {
		name = name[:200]
	}
	name = strings.TrimRight(strings.TrimSpace(name), ". ")
	if name == "" || name == "." || name == ".." {
		return "download"
	}
	return name
}
