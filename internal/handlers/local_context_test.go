package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentDirOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Movies/2024/Inception.mkv", filepath.Join("Movies", "2024")},
		{"Inception.mkv", ""},   // at mount root → no dir hint
		{"", ""},                // empty
		{".", ""},               // sentinel
		{"a/b/c/d.mkv", filepath.Join("a", "b", "c")},
	}
	for _, tc := range cases {
		if got := currentDirOf(tc.in); got != tc.want {
			t.Errorf("currentDirOf(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestLocalContextFor_ReadsTopLevelFolders(t *testing.T) {
	base := t.TempDir()
	for _, d := range []string{"Movies", "Series", "Anime", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A file at the root must NOT be listed as a folder.
	if err := os.WriteFile(filepath.Join(base, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	lc := localContextFor(base, "media", "Movies/2024")
	if lc == nil {
		t.Fatal("localContextFor returned nil for a readable base")
	}
	if lc.CurrentPath != "Movies/2024" || lc.MountName != "media" {
		t.Errorf("path/mount = %q/%q, want Movies/2024/media", lc.CurrentPath, lc.MountName)
	}
	got := map[string]bool{}
	for _, f := range lc.DestFolders {
		got[f] = true
	}
	for _, want := range []string{"Movies", "Series", "Anime"} {
		if !got[want] {
			t.Errorf("DestFolders missing %q: %v", want, lc.DestFolders)
		}
	}
	if got[".hidden"] {
		t.Errorf("DestFolders leaked a dotfile: %v", lc.DestFolders)
	}
	if got["readme.txt"] {
		t.Errorf("DestFolders leaked a file: %v", lc.DestFolders)
	}
}

func TestLocalContextFor_MissingBaseDegrades(t *testing.T) {
	// Unreadable base + a known current path → context with no folders (still
	// gives the AI a location hint, never panics).
	lc := localContextFor(filepath.Join(t.TempDir(), "does-not-exist"), "media", "Movies")
	if lc == nil {
		t.Fatal("expected non-nil context when currentPath is known")
	}
	if len(lc.DestFolders) != 0 {
		t.Errorf("DestFolders = %v, want empty", lc.DestFolders)
	}
	if lc.CurrentPath != "Movies" {
		t.Errorf("CurrentPath = %q, want Movies", lc.CurrentPath)
	}
}

func TestLocalContextFor_MissingBaseNoPathIsNil(t *testing.T) {
	// Nothing useful → nil, so the renamer falls back to legacy labels.
	lc := localContextFor(filepath.Join(t.TempDir(), "nope"), "media", "")
	if lc != nil {
		t.Errorf("expected nil context, got %+v", lc)
	}
}

func TestLocalContextFor_TruncatesManyFolders(t *testing.T) {
	base := t.TempDir()
	for i := 0; i < maxPromoteContextFolders+10; i++ {
		name := filepath.Join(base, fmt.Sprintf("dir%03d", i))
		if err := os.MkdirAll(name, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lc := localContextFor(base, "media", "")
	if lc == nil {
		t.Fatal("nil context")
	}
	if len(lc.DestFolders) > maxPromoteContextFolders {
		t.Errorf("DestFolders len = %d, want <= %d", len(lc.DestFolders), maxPromoteContextFolders)
	}
}
