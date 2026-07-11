package local

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MigratedEntry records one relocated root entry during a UserSubpath migration.
type MigratedEntry struct {
	Name     string // the entry's filename at the (old) mount root
	ToUser   string // the username subdir it was moved into
	Fallback bool   // true when attribution failed and it went to fallbackUser
}

// MigrationResult summarizes a MigrateToUserSubpath run.
type MigrationResult struct {
	Mount     string
	Moved     []MigratedEntry
	Skipped   int // entries already inside a known user's subdir (idempotency)
	Active    int // entries left in place because they're downloads in progress (.part)
	Conflicts int // entries left in place because the per-user dest already exists
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
		if err := migrateRootEntry(mountAbs, de, knownUsers, fallbackUser, attribute, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// migrateRootEntry decides what to do with one root entry and applies it,
// tallying the outcome into res. Split out of MigrateToUserSubpath to keep that
// function's cognitive complexity in check. Returns an error only for a real I/O
// failure (which aborts the whole migration); every "leave in place" decision is
// a counted no-op, not an error.
func migrateRootEntry(mountAbs string, de os.DirEntry, knownUsers map[string]bool, fallbackUser string, attribute func(absPath string) (string, bool), res *MigrationResult) error {
	name := de.Name()
	srcAbs := filepath.Join(mountAbs, name)

	// An existing user's subdir is already scoped → leave it (idempotency).
	if de.IsDir() && knownUsers[name] {
		res.Skipped++
		return nil
	}
	// A download in progress (anacrolix leaves <file>.part until a piece is
	// complete) must NEVER be relocated: moving the tree out from under the live
	// torrent strands its pieces and forces a full re-download.
	if de.IsDir() && hasPartFiles(srcAbs) {
		res.Active++
		return nil
	}

	user, fallback := resolveOwner(srcAbs, knownUsers, fallbackUser, attribute)
	if user == "" {
		return nil // no owner and no fallback — leave in place rather than lose track
	}

	moved, err := moveIntoUserSubdir(mountAbs, user, name)
	if err != nil {
		return err
	}
	if !moved {
		// The per-user destination already exists. Appending " (N)" here is what
		// spawned the duplicate "name (1)", "name (2)" folders (and orphaned data)
		// when this raced a live download. Leave the entry at the root instead —
		// still browsable, never duplicated — and surface it for manual cleanup.
		res.Conflicts++
		return nil
	}
	res.Moved = append(res.Moved, MigratedEntry{Name: name, ToUser: user, Fallback: fallback})
	return nil
}

// hasPartFiles reports whether dir contains an anacrolix ".part" file anywhere in
// its tree — the signature of a download still in progress. Walk stops at the
// first hit; walk errors are treated as "no .part" (best-effort, never blocks).
func hasPartFiles(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".part") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
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

// moveIntoUserSubdir relocates mountAbs/name into mountAbs/user/. Returns
// moved=false (without error) when mountAbs/user/name already exists: the caller
// leaves the source at the root rather than minting a numbered duplicate, which
// previously orphaned the in-flight download's data. err is reserved for real I/O
// failures.
func moveIntoUserSubdir(mountAbs, user, name string) (moved bool, err error) {
	destDir := filepath.Join(mountAbs, user)
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return false, err
	}
	dest := filepath.Join(destDir, name)
	if _, err := os.Lstat(dest); err == nil {
		return false, nil // destination taken — don't duplicate
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.Rename(filepath.Join(mountAbs, name), dest); err != nil {
		return false, err
	}
	return true, nil
}
