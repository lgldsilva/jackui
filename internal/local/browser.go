package local

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lgldsilva/jackui/internal/config"
)

type Entry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	IsDir      bool      `json:"isDir"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"modTime"`
	IsPlayable bool      `json:"isPlayable"`
	// ChildCount is the number of (non-hidden) entries directly inside a
	// directory — shown in the UI where files show their size. 0 for files.
	ChildCount int `json:"childCount"`
}

type Mount struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	UserSubpath bool   `json:"userSubpath"` // per-user subdirs — drives the admin "view as user" selector
	Restricted  bool   `json:"restricted"`  // visible only to specific users (AllowedUsers non-empty); names NOT exposed here
}

type Browser struct {
	mu     sync.RWMutex
	mounts []config.ExternalMount
}

func NewBrowser(mounts []config.ExternalMount) *Browser {
	return &Browser{mounts: mounts}
}

// snapshot returns the current mount slice under a read lock. SetMounts replaces
// the slice wholesale (never mutates in place), so iterating the returned slice
// without holding the lock is safe.
func (b *Browser) snapshot() []config.ExternalMount {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.mounts
}

// SetMounts swaps the live mount configuration (used by the admin mounts editor
// so changes apply without a restart).
func (b *Browser) SetMounts(mounts []config.ExternalMount) {
	b.mu.Lock()
	b.mounts = mounts
	b.mu.Unlock()
}

// Config returns a copy of the raw mount config (admin-only; includes AllowedUsers).
func (b *Browser) Config() []config.ExternalMount {
	src := b.snapshot()
	out := make([]config.ExternalMount, len(src))
	copy(out, src)
	return out
}

func (b *Browser) Mounts() []Mount {
	return b.MountsFor("")
}

// MountsFor returns mounts visible to the given username.
// Empty username = only public mounts (AllowedUsers empty).
func (b *Browser) MountsFor(username string) []Mount {
	mounts := b.snapshot()
	out := make([]Mount, 0, len(mounts))
	for _, m := range mounts {
		visible := len(m.AllowedUsers) == 0
		if !visible && username != "" {
			for _, u := range m.AllowedUsers {
				if u == username {
					visible = true
					break
				}
			}
		}
		if !visible {
			continue
		}
		out = append(out, Mount{Name: m.Name, Path: m.Path, UserSubpath: m.UserSubpath, Restricted: len(m.AllowedUsers) > 0})
	}
	return out
}

// UserCanAccess checks if a username is allowed to access a given mount name.
func (b *Browser) UserCanAccess(username, mountName string) bool {
	for _, m := range b.snapshot() {
		if m.Name == mountName {
			if len(m.AllowedUsers) == 0 {
				return true
			}
			for _, u := range m.AllowedUsers {
				if u == username {
					return true
				}
			}
			return false
		}
	}
	return false
}

func (b *Browser) findMount(name string) (config.ExternalMount, bool) {
	for _, m := range b.snapshot() {
		if m.Name == name {
			return m, true
		}
	}
	return config.ExternalMount{}, false
}

// effectivePath returns the effective root path for a mount + username combination.
// For UserSubpath mounts, this is m.Path/{username}; otherwise m.Path.
func effectivePath(m config.ExternalMount, username string) string {
	if m.UserSubpath && username != "" {
		return filepath.Join(m.Path, username)
	}
	return m.Path
}

// ResolvePath joins mount.Path with relPath safely, rejecting any attempt
// to escape the mount root via "..", absolute paths, or symlink-like trickery.
// Returns the absolute path on disk.
func (b *Browser) ResolvePath(mountName, relPath string) (string, error) {
	return b.ResolvePathFor(mountName, relPath, "")
}

// ResolvePathFor is like ResolvePath but respects UserSubpath mounts for the given user.
func (b *Browser) ResolvePathFor(mountName, relPath, username string) (string, error) {
	mount, ok := b.findMount(mountName)
	if !ok {
		return "", fmt.Errorf("mount %q not found", mountName)
	}
	mount.Path = effectivePath(mount, username)

	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("path must be relative to mount root")
	}

	if hasPathTraversal(relPath) {
		return "", fmt.Errorf("path traversal rejected")
	}

	clean := filepath.Clean("/" + relPath)
	clean = strings.TrimPrefix(clean, "/")

	mountAbs, err := filepath.Abs(mount.Path)
	if err != nil {
		return "", fmt.Errorf("invalid mount path: %w", err)
	}

	abs := filepath.Join(mountAbs, clean)

	if !isUnderDir(abs, mountAbs) {
		return "", fmt.Errorf("path traversal rejected")
	}

	mountReal := symlinkOrSelf(mountAbs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		if !isUnderDir(resolved, mountReal) {
			return "", fmt.Errorf("path traversal rejected (symlink escape)")
		}
	}

	return abs, nil
}

// IsUserSubpath reports whether the named mount uses per-user subdirectories.
func (b *Browser) IsUserSubpath(mountName string) bool {
	m, ok := b.findMount(mountName)
	return ok && m.UserSubpath
}

// UserScopedPath prepends the username to relPath for UserSubpath mounts.
// For regular mounts, returns relPath unchanged. Safe to use on all mounts.
func (b *Browser) UserScopedPath(mountName, relPath, username string) string {
	if !b.IsUserSubpath(mountName) || username == "" {
		return relPath
	}
	if relPath == "" || relPath == "." {
		return username
	}
	return username + "/" + relPath
}

// StripUserScope removes the leading username prefix from Entry paths for
// UserSubpath mounts, so the frontend sees paths as if the mount root were
// the user's personal subdir.
func (b *Browser) StripUserScope(mountName, username string, entries []Entry) []Entry {
	if !b.IsUserSubpath(mountName) || username == "" {
		return entries
	}
	prefix := username + "/"
	for i := range entries {
		entries[i].Path = strings.TrimPrefix(entries[i].Path, prefix)
	}
	return entries
}

// MigratedEntry records one relocated root entry during a UserSubpath migration.
type MigratedEntry struct {
	Name     string // the entry's filename at the (old) mount root
	ToUser   string // the username subdir it was moved into
	Fallback bool   // true when attribution failed and it went to fallbackUser
}

// MigrationResult summarizes a MigrateToUserSubpath run.
type MigrationResult struct {
	Mount   string
	Moved   []MigratedEntry
	Skipped int // entries already inside a known user's subdir (idempotency)
}

// MigrateToUserSubpath relocates loose entries at a UserSubpath mount's root
// into per-user subdirs (mount/{username}/...), so flipping a previously-shared
// mount to per-user doesn't orphan existing files.
//
// attribute maps an entry's absolute path to (username, true) when an owner is
// known; entries it can't attribute (or whose owner isn't a known user) go to
// fallbackUser. knownUsers is the set of valid usernames: a root *directory*
// whose name is a known user is treated as already-scoped and left untouched,
// which makes the operation idempotent (safe to run on every boot). Moves use
// os.Rename — atomic and copy-free since source and dest share the mount root.
func (b *Browser) MigrateToUserSubpath(mountName string, knownUsers map[string]bool, fallbackUser string, attribute func(absPath string) (string, bool)) (MigrationResult, error) {
	res := MigrationResult{Mount: mountName}
	m, ok := b.findMount(mountName)
	if !ok {
		return res, fmt.Errorf("mount %q not found", mountName)
	}
	if !m.UserSubpath {
		return res, nil // mount is shared — nothing to scope
	}
	mountAbs, err := filepath.Abs(m.Path)
	if err != nil {
		return res, fmt.Errorf("invalid mount path: %w", err)
	}
	entries, err := os.ReadDir(mountAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return res, err
	}

	for _, de := range entries {
		name := de.Name()
		// An existing user's subdir is already scoped → leave it (idempotency).
		if de.IsDir() && knownUsers[name] {
			res.Skipped++
			continue
		}

		user, fallback := resolveOwner(filepath.Join(mountAbs, name), knownUsers, fallbackUser, attribute)
		if user == "" {
			// No owner and no fallback — leave in place rather than lose track.
			continue
		}

		if err := moveIntoUserSubdir(mountAbs, user, name); err != nil {
			return res, err
		}
		res.Moved = append(res.Moved, MigratedEntry{Name: name, ToUser: user, Fallback: fallback})
	}
	return res, nil
}

// resolveOwner attributes abs to a known user, falling back to fallbackUser
// (with fallback=true) when attribution fails or names an unknown user. Returns
// an empty user when there's neither an owner nor a fallback.
func resolveOwner(abs string, knownUsers map[string]bool, fallbackUser string, attribute func(absPath string) (string, bool)) (user string, fallback bool) {
	user, attributed := attribute(abs)
	if !attributed || user == "" || !knownUsers[user] {
		return fallbackUser, true
	}
	return user, false
}

// moveIntoUserSubdir relocates mountAbs/name into mountAbs/user/, creating the
// subdir and avoiding collisions with any file already there.
func moveIntoUserSubdir(mountAbs, user, name string) error {
	destDir := filepath.Join(mountAbs, user)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	return os.Rename(filepath.Join(mountAbs, name), nonCollidingPath(destDir, name))
}

// nonCollidingPath returns dir/name, or dir/name (n).ext if that already exists,
// so a migration never overwrites a file already in the destination subdir.
func nonCollidingPath(dir, name string) string {
	dest := filepath.Join(dir, name)
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		return dest
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
	return dest
}

func hasPathTraversal(relPath string) bool {
	if relPath == "" {
		return false
	}
	normalized := strings.ReplaceAll(relPath, "\\", "/")
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func isUnderDir(abs, dir string) bool {
	return abs == dir || strings.HasPrefix(abs, dir+string(os.PathSeparator))
}

func symlinkOrSelf(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

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

// List returns directory entries at relPath inside the given mount.
// Files are flagged isPlayable when extension matches a known video/audio type.
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
		info, err := de.Info()
		if err != nil {
			continue
		}
		name := de.Name()
		// Skip hidden files starting with "."
		if strings.HasPrefix(name, ".") {
			continue
		}

		var p string
		if prefix == "" {
			p = name
		} else {
			p = prefix + "/" + name
		}

		isDir := de.IsDir()
		out = append(out, Entry{
			Name:       name,
			Path:       p,
			IsDir:      isDir,
			Size:       info.Size(),
			ModTime:    info.ModTime(),
			IsPlayable: !isDir && IsPlayable(name),
			ChildCount: childCount(isDir, filepath.Join(abs, name)),
		})
	}

	// Directories first, then files; alphabetical within each group.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	return out, nil
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
