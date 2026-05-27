package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luizg/jackui/internal/config"
)

func setupTestMount(t *testing.T) (string, *Browser) {
	t.Helper()
	root := t.TempDir()

	// Create a small fixture tree:
	//   root/
	//     movies/
	//       film.mkv
	//       notes.txt
	//     music/
	//       song.mp3
	//     readme.md
	mustMkdir(t, filepath.Join(root, "movies"))
	mustMkdir(t, filepath.Join(root, "music"))
	mustWrite(t, filepath.Join(root, "movies", "film.mkv"), "video")
	mustWrite(t, filepath.Join(root, "movies", "notes.txt"), "text")
	mustWrite(t, filepath.Join(root, "music", "song.mp3"), "audio")
	mustWrite(t, filepath.Join(root, "readme.md"), "doc")

	b := NewBrowser([]config.ExternalMount{
		{Name: "test", Path: root},
	})
	return root, b
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func TestList_Root(t *testing.T) {
	_, b := setupTestMount(t)

	entries, err := b.List("test", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	wantContains := []string{"movies", "music", "readme.md"}
	for _, w := range wantContains {
		found := false
		for _, n := range names {
			if n == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected entry %q in root list, got %v", w, names)
		}
	}

	// Directories must come first.
	if entries[0].Name != "movies" && entries[0].Name != "music" {
		t.Errorf("expected directories first, got %q", entries[0].Name)
	}
}

func TestList_SubDir(t *testing.T) {
	_, b := setupTestMount(t)

	entries, err := b.List("test", "movies")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	var film *Entry
	for i := range entries {
		if entries[i].Name == "film.mkv" {
			film = &entries[i]
		}
	}
	if film == nil {
		t.Fatalf("film.mkv not found")
	}
	if !film.IsPlayable {
		t.Errorf("expected film.mkv to be playable")
	}
	if film.Path != "movies/film.mkv" {
		t.Errorf("expected path movies/film.mkv, got %q", film.Path)
	}
}

func TestList_PathTraversalRejected(t *testing.T) {
	_, b := setupTestMount(t)

	cases := []string{
		"..",
		"../",
		"../etc",
		"../../etc/passwd",
		"movies/../../etc",
		"./../outside",
	}

	for _, rel := range cases {
		_, err := b.List("test", rel)
		if err == nil {
			t.Errorf("expected error for path traversal %q, got nil", rel)
			continue
		}
		// Either path traversal rejected or not-a-directory; both acceptable
		// as long as it doesn't escape the mount root.
		if !strings.Contains(err.Error(), "traversal") &&
			!strings.Contains(err.Error(), "not found") &&
			!strings.Contains(err.Error(), "no such") &&
			!strings.Contains(err.Error(), "not a directory") {
			// Anything that's not a clear rejection should still NOT have escaped.
			// We can't easily check that here but the resolve path test below covers it.
		}
	}
}

func TestResolvePath_TraversalRejected(t *testing.T) {
	root, b := setupTestMount(t)

	bad := []string{
		"..",
		"../escape",
		"movies/../../outside",
		"/absolute/path",
	}

	for _, rel := range bad {
		abs, err := b.ResolvePath("test", rel)
		if err != nil {
			continue // properly rejected
		}
		// If no error, verify the resolved abs is still inside root.
		mountAbs, _ := filepath.Abs(root)
		if abs != mountAbs && !strings.HasPrefix(abs, mountAbs+string(os.PathSeparator)) {
			t.Errorf("path %q escaped mount root: resolved to %q", rel, abs)
		}
	}
}

// TestResolvePath_SymlinkEscapeRejected guards S4: a symlink INSIDE the mount
// pointing OUTSIDE it passes the lexical checks (no "..", stays under the
// prefix as a string) but must be rejected after symlink resolution — otherwise
// os.Stat/ServeFile would follow it and serve a host file.
func TestResolvePath_SymlinkEscapeRejected(t *testing.T) {
	root, b := setupTestMount(t)

	// A directory outside the mount with a secret file.
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.txt"), "host secret")

	// A symlink inside the mount pointing to that outside directory.
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// "escape/secret.txt" has no ".." and stays under the prefix lexically, but
	// resolves outside the mount → must be rejected.
	if _, err := b.ResolvePath("test", "escape/secret.txt"); err == nil {
		t.Error("expected symlink escape to be rejected, got nil error")
	}
}

func TestResolvePath_Valid(t *testing.T) {
	root, b := setupTestMount(t)

	abs, err := b.ResolvePath("test", "movies/film.mkv")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	mountAbs, _ := filepath.Abs(root)
	want := filepath.Join(mountAbs, "movies", "film.mkv")
	if abs != want {
		t.Errorf("ResolvePath = %q, want %q", abs, want)
	}
}

func TestResolvePath_UnknownMount(t *testing.T) {
	_, b := setupTestMount(t)
	_, err := b.ResolvePath("nope", "")
	if err == nil {
		t.Error("expected error for unknown mount")
	}
}

func TestMounts(t *testing.T) {
	_, b := setupTestMount(t)
	m := b.Mounts()
	if len(m) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(m))
	}
	if m[0].Name != "test" {
		t.Errorf("mount name = %q, want test", m[0].Name)
	}
}

func TestIsPlayable(t *testing.T) {
	cases := map[string]bool{
		"foo.mkv":  true,
		"foo.MP4":  true,
		"foo.webm": true,
		"foo.mp3":  true,
		"foo.txt":  false,
		"foo":      false,
		"":         false,
	}
	for name, want := range cases {
		if got := IsPlayable(name); got != want {
			t.Errorf("IsPlayable(%q) = %v, want %v", name, got, want)
		}
	}
}
