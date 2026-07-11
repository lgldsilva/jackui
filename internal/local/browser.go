package local

import (
	"fmt"
	"os"
	"path/filepath"
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
	// Locked marks a directory the user pinned (a ".keep" marker inside) so
	// "clean empty folders" never removes it even when it holds no files.
	Locked bool `json:"locked,omitempty"`
	// Incomplete flags a download still in progress: a ".part" file, or a
	// directory whose tree still holds one. Lets the browser show a "baixando"
	// indicator instead of presenting a half-written file as if it were ready.
	Incomplete bool `json:"incomplete,omitempty"`
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

// effectiveMounts is the slice EVERY read path must iterate: the raw snapshot
// with duplicate names collapsed (restricted twin wins). Listing (MountsFor),
// authorization (UserCanAccess) and path resolution (findMount) all go through
// it, so a stray duplicate can never widen visibility OR file access.
func (b *Browser) effectiveMounts() []config.ExternalMount {
	return dedupePreferRestricted(b.snapshot())
}

// MountsFor returns mounts visible to the given username.
// Empty username = only public mounts (AllowedUsers empty).
func (b *Browser) MountsFor(username string) []Mount {
	mounts := b.effectiveMounts()
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

// dedupePreferRestricted collapses duplicate mount names (defense in depth —
// config-level dedupe should prevent them, but a stray twin must never WIDEN
// visibility): when two entries share a name, the restricted one (AllowedUsers
// non-empty) wins over an unrestricted duplicate.
func dedupePreferRestricted(mounts []config.ExternalMount) []config.ExternalMount {
	byName := make(map[string]int, len(mounts))
	out := make([]config.ExternalMount, 0, len(mounts))
	for _, m := range mounts {
		key := strings.ToLower(strings.TrimSpace(m.Name))
		idx, dup := byName[key]
		if !dup {
			byName[key] = len(out)
			out = append(out, m)
			continue
		}
		if len(out[idx].AllowedUsers) == 0 && len(m.AllowedUsers) > 0 {
			out[idx] = m
		}
	}
	return out
}

// UserCanAccess checks if a username is allowed to access a given mount name.
// It iterates effectiveMounts (NOT the raw snapshot) so an unrestricted
// duplicate can never grant access that its restricted twin denies.
func (b *Browser) UserCanAccess(username, mountName string) bool {
	for _, m := range b.effectiveMounts() {
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

// findMount resolves a mount by exact name over effectiveMounts, so duplicate
// names always resolve to the restricted twin (same view UserCanAccess uses).
func (b *Browser) findMount(name string) (config.ExternalMount, bool) {
	for _, m := range b.effectiveMounts() {
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
