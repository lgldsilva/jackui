package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// List returns directory entries at relPath inside the given mount.
// Files are flagged isPlayable when extension matches a known video/audio type.
func (b *Browser) List(mountName, relPath string) ([]Entry, error) {
	return b.ListContext(context.Background(), mountName, relPath)
}

// ListContext is List bound to a request: a cancelled ctx stops the listing
// between entries and returns ctx.Err().
func (b *Browser) ListContext(ctx context.Context, mountName, relPath string) ([]Entry, error) {
	return b.list(newListScan(ctx, os.ReadDir), mountName, relPath)
}

func (b *Browser) list(scan *listScan, mountName, relPath string) ([]Entry, error) {
	abs, err := b.ResolvePath(mountName, relPath)
	if err != nil {
		return nil, err
	}

	stat, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("not a directory")
	}

	dirEntries, err := scan.readDir(abs)
	if err != nil {
		return nil, err
	}

	// Clean relPath used as prefix for entry.Path
	prefix := filepath.Clean("/" + relPath)
	prefix = strings.TrimPrefix(prefix, "/")

	out := make([]Entry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if err := scan.ctx.Err(); err != nil {
			return nil, err
		}
		if entry, ok := scan.buildEntry(abs, prefix, de); ok {
			out = append(out, entry)
		}
	}

	// Directories first, then files; alphabetical within each group.
	sort.SliceStable(out, func(i, j int) bool { return lessEntry(out[i], out[j]) })

	return out, nil
}

// buildEntry materializes a directory entry, returning ok=false for entries
// that should be skipped (unreadable info or hidden files starting with ".").
func (s *listScan) buildEntry(abs, prefix string, de os.DirEntry) (Entry, bool) {
	info, err := de.Info()
	if err != nil {
		return Entry{}, false
	}
	name := de.Name()
	if strings.HasPrefix(name, ".") {
		return Entry{}, false
	}

	entry := Entry{
		Name:    name,
		Path:    entryPath(prefix, name),
		IsDir:   de.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
	if !entry.IsDir {
		entry.IsPlayable = IsPlayable(name)
		// A bare anacrolix ".part" is a download still in progress.
		entry.Incomplete = strings.HasSuffix(name, ".part")
		return entry, true
	}
	childAbs := filepath.Join(abs, name)
	entry.ChildCount, entry.Incomplete = s.inspectDir(childAbs)
	entry.Locked = isFolderLocked(childAbs)
	return entry, true
}

// entryPath joins the cleaned relPath prefix with an entry name.
func entryPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

// lessEntry orders directories before files, alphabetical within each group.
func lessEntry(a, b Entry) bool {
	if a.IsDir != b.IsDir {
		return a.IsDir
	}
	return strings.ToLower(a.Name) < strings.ToLower(b.Name)
}

var playableExtensions = map[string]bool{
	".mp4":  true,
	".m4v":  true,
	".mkv":  true,
	".avi":  true,
	".mov":  true,
	".wmv":  true,
	".webm": true,
	".flv":  true,
	".mpeg": true,
	".mpg":  true,
	".ts":   true,
	".m2ts": true,
	".mp3":  true,
	".m4a":  true,
	".aac":  true,
	".flac": true,
	".ogg":  true,
	".wav":  true,
	".opus": true,
}

// IsPlayable returns true if the filename's extension is a known video/audio type.
func IsPlayable(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return playableExtensions[ext]
}
