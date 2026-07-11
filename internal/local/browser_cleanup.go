package local

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// keepMarker is the empty-but-present file that pins a folder: a dir holding it
// is no longer "empty" to RemoveEmptyDirs (os.Remove → ENOTEMPTY), so the
// cleanup leaves it alone. List hides it (dotfile) and surfaces Entry.Locked.
const keepMarker = ".keep"

// Walk recursively lists all files (not dirs) under relPath inside the given mount.
// If mediaOnly is true, only files with a playable extension are returned.
// Paths in each Entry are relative to the mount root — same format as List.
func (b *Browser) Walk(mountName, relPath string, mediaOnly bool) ([]Entry, error) {
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

	mountAbs, err := filepath.Abs(b.findMountPath(mountName))
	if err != nil {
		mountAbs = abs
	}

	var out []Entry
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, walkErr error) error {
		return collectWalkEntry(&out, mountAbs, path, d, walkErr, mediaOnly)
	})
	return out, err
}

func collectWalkEntry(out *[]Entry, mountAbs, path string, d fs.DirEntry, walkErr error, mediaOnly bool) error {
	if walkErr != nil {
		return nil
	}
	if d.IsDir() {
		if strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		return nil
	}
	if strings.HasPrefix(d.Name(), ".") {
		return nil
	}
	if mediaOnly && !IsPlayable(d.Name()) {
		return nil
	}
	info, err := d.Info()
	if err != nil {
		return nil
	}
	rel, err := filepath.Rel(mountAbs, path)
	if err != nil {
		rel = path
	}
	*out = append(*out, Entry{
		Name:       d.Name(),
		Path:       filepath.ToSlash(rel),
		IsDir:      false,
		Size:       info.Size(),
		ModTime:    info.ModTime(),
		IsPlayable: IsPlayable(d.Name()),
	})
	return nil
}

// RemoveEmptyDirs deletes empty subdirectories under relPath inside the mount,
// bottom-up so a directory that only held now-removed empty children is itself
// removed. Returns how many directories were deleted.
//
// Safety: the start directory itself and the mount root are never removed;
// hidden dirs (".thumbs", ".artwork", …) are skipped entirely; os.Remove only
// succeeds on a truly empty dir (non-empty → ENOTEMPTY, ignored). Path traversal
// is already guarded by ResolvePath.
func (b *Browser) RemoveEmptyDirs(mountName, relPath string) (int, error) {
	startAbs, err := b.ResolvePath(mountName, relPath)
	if err != nil {
		return 0, err
	}
	stat, err := os.Stat(startAbs)
	if err != nil {
		return 0, err
	}
	if !stat.IsDir() {
		return 0, fmt.Errorf("not a directory")
	}
	mountAbs, err := filepath.Abs(b.findMountPath(mountName))
	if err != nil {
		mountAbs = startAbs
	}

	removed := 0
	for _, dir := range collectDirsDeepestFirst(startAbs) {
		if dir == startAbs || dir == mountAbs {
			continue // never delete the starting dir or the mount root
		}
		if os.Remove(dir) == nil { // only succeeds on a truly empty dir
			removed++
		}
	}
	return removed, nil
}

// isFolderLocked reports whether dirAbs holds the keep marker.
func isFolderLocked(dirAbs string) bool {
	_, err := os.Stat(filepath.Join(dirAbs, keepMarker))
	return err == nil
}

// SetFolderLock pins (locked=true) or unpins a directory by creating/removing
// the keep marker inside it. Pinned folders survive RemoveEmptyDirs. The path
// must resolve to a directory inside the mount (ResolvePath guards traversal);
// the mount root itself can't be pinned. Idempotent: locking an already-locked
// folder (or unlocking an unlocked one) is a no-op success.
func (b *Browser) SetFolderLock(mountName, relPath string, locked bool) error {
	abs, err := b.ResolvePath(mountName, relPath)
	if err != nil {
		return err
	}
	mountAbs, err := filepath.Abs(b.findMountPath(mountName))
	if err == nil && abs == mountAbs {
		return fmt.Errorf("cannot lock the mount root")
	}
	stat, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !stat.IsDir() {
		return fmt.Errorf("not a directory")
	}
	marker := filepath.Join(abs, keepMarker)
	if locked {
		// #nosec G304 G302 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna; arquivo de midia; 0644 intencional p/ leitura
		f, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		return f.Close()
	}
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// collectDirsDeepestFirst lists directories under root (skipping hidden trees),
// ordered deepest-path-first so a parent is visited after its children — which
// is what lets RemoveEmptyDirs cascade upward as children are removed.
func collectDirsDeepestFirst(root string) []string {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		return collectCleanupDir(&dirs, root, path, d, walkErr)
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	return dirs
}

func collectCleanupDir(dirs *[]string, root, path string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil || !d.IsDir() {
		return nil
	}
	if path != root && strings.HasPrefix(d.Name(), ".") {
		return filepath.SkipDir // never descend into / remove internal dotdirs
	}
	*dirs = append(*dirs, path)
	return nil
}

// findMountPath returns the root path of a named mount (or empty string).
func (b *Browser) findMountPath(name string) string {
	m, ok := b.findMount(name)
	if !ok {
		return ""
	}
	return m.Path
}
