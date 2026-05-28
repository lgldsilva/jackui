package local

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luizg/jackui/internal/config"
)

type Entry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	IsDir      bool      `json:"isDir"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"modTime"`
	IsPlayable bool      `json:"isPlayable"`
}

type Mount struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Browser struct {
	mounts []config.ExternalMount
}

func NewBrowser(mounts []config.ExternalMount) *Browser {
	return &Browser{mounts: mounts}
}

func (b *Browser) Mounts() []Mount {
	return b.MountsFor("")
}

// MountsFor returns mounts visible to the given username.
// Empty username = only public mounts (AllowedUsers empty).
func (b *Browser) MountsFor(username string) []Mount {
	out := make([]Mount, 0, len(b.mounts))
	for _, m := range b.mounts {
		if len(m.AllowedUsers) == 0 {
			out = append(out, Mount{Name: m.Name, Path: m.Path})
		} else if username != "" {
			for _, u := range m.AllowedUsers {
				if u == username {
					out = append(out, Mount{Name: m.Name, Path: m.Path})
					break
				}
			}
		}
	}
	return out
}

// UserCanAccess checks if a username is allowed to access a given mount name.
func (b *Browser) UserCanAccess(username, mountName string) bool {
	for _, m := range b.mounts {
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
	for _, m := range b.mounts {
		if m.Name == name {
			return m, true
		}
	}
	return config.ExternalMount{}, false
}

// ResolvePath joins mount.Path with relPath safely, rejecting any attempt
// to escape the mount root via "..", absolute paths, or symlink-like trickery.
// Returns the absolute path on disk.
func (b *Browser) ResolvePath(mountName, relPath string) (string, error) {
	mount, ok := b.findMount(mountName)
	if !ok {
		return "", fmt.Errorf("mount %q not found", mountName)
	}

	// Reject absolute relPath outright.
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("path must be relative to mount root")
	}

	// Reject any ".." segment up front — even segments that would Clean back
	// inside (e.g. "movies/../movies") are rejected to keep the rule simple
	// and unambiguous.
	if relPath != "" {
		// Normalize separators to forward slash for inspection (URL-style),
		// since callers will typically pass slash-separated paths from the UI.
		normalized := strings.ReplaceAll(relPath, "\\", "/")
		for _, segment := range strings.Split(normalized, "/") {
			if segment == ".." {
				return "", fmt.Errorf("path traversal rejected")
			}
		}
	}

	// Clean & normalize (collapse "." and double slashes).
	clean := filepath.Clean("/" + relPath)
	clean = strings.TrimPrefix(clean, "/")

	// Build absolute candidate.
	mountAbs, err := filepath.Abs(mount.Path)
	if err != nil {
		return "", fmt.Errorf("invalid mount path: %w", err)
	}

	abs := filepath.Join(mountAbs, clean)

	// Defense in depth: verify the lexical path is still inside the mount.
	if abs != mountAbs && !strings.HasPrefix(abs, mountAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("path traversal rejected")
	}

	// The lexical check above does NOT catch a symlink INSIDE the mount that
	// points OUTSIDE it (e.g. mount/x -> /etc): the string "x/passwd" has no
	// ".." and stays under the prefix, but os.Stat/ServeFile would follow the
	// link and serve a host file. Resolve symlinks and re-validate against the
	// REAL mount path. EvalSymlinks needs the target to exist; if it doesn't yet
	// (a path that will 404 anyway), keep the lexical abs — it's already
	// prefix-validated and a missing path fails downstream.
	mountReal, err := filepath.EvalSymlinks(mountAbs)
	if err != nil {
		mountReal = mountAbs
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		if resolved != mountReal && !strings.HasPrefix(resolved, mountReal+string(os.PathSeparator)) {
			return "", fmt.Errorf("path traversal rejected (symlink escape)")
		}
		// Validate with the resolved path, but RETURN the lexical abs — resolving
		// would also rewrite benign system symlinks (e.g. macOS /var→/private/var),
		// changing the path for no security benefit. Since the escape check above
		// passed, the lexical path points safely inside the mount.
	}

	return abs, nil
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
