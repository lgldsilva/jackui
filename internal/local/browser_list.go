package local

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// childCount returns the number of non-hidden entries directly inside dirAbs,
// or 0 for files / on error. Best-effort: one ReadDir per directory in a
// listing. On rclone/network mounts this leans on the VFS dir cache; if it ever
// shows up as slow there, make it lazy (count on expand) rather than eager.
func childCount(isDir bool, dirAbs string) int {
	if !isDir {
		return 0
	}
	des, err := os.ReadDir(dirAbs)
	if err != nil {
		return 0
	}
	n := 0
	for _, de := range des {
		if !strings.HasPrefix(de.Name(), ".") {
			n++
		}
	}
	return n
}

// List returns directory entries at relPath inside the given mount.
// Files are flagged isPlayable when extension matches a known video/audio type.
func (b *Browser) List(mountName, relPath string) ([]Entry, error) {
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

	dirEntries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}

	// Clean relPath used as prefix for entry.Path
	prefix := filepath.Clean("/" + relPath)
	prefix = strings.TrimPrefix(prefix, "/")

	out := make([]Entry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if entry, ok := buildEntry(abs, prefix, de); ok {
			out = append(out, entry)
		}
	}

	// Directories first, then files; alphabetical within each group.
	sort.SliceStable(out, func(i, j int) bool { return lessEntry(out[i], out[j]) })

	return out, nil
}

// buildEntry materializes a directory entry, returning ok=false for entries
// that should be skipped (unreadable info or hidden files starting with ".").
func buildEntry(abs, prefix string, de os.DirEntry) (Entry, bool) {
	info, err := de.Info()
	if err != nil {
		return Entry{}, false
	}
	name := de.Name()
	if strings.HasPrefix(name, ".") {
		return Entry{}, false
	}

	isDir := de.IsDir()
	childAbs := filepath.Join(abs, name)
	return Entry{
		Name:       name,
		Path:       entryPath(prefix, name),
		IsDir:      isDir,
		Size:       info.Size(),
		ModTime:    info.ModTime(),
		IsPlayable: !isDir && IsPlayable(name),
		ChildCount: childCount(isDir, childAbs),
		Locked:     isDir && isFolderLocked(childAbs),
		Incomplete: entryIncomplete(isDir, name, childAbs),
	}, true
}

// entryIncomplete reports whether an entry is a download still in progress: a
// file is incomplete when it's an anacrolix ".part"; a directory is incomplete
// when its tree still holds one (hasPartFiles short-circuits on the first hit).
func entryIncomplete(isDir bool, name, abs string) bool {
	if !isDir {
		return strings.HasSuffix(name, ".part")
	}
	return hasPartFiles(abs)
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
