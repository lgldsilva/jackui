package local

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

func TestRemoveEmptyDirs(t *testing.T) {
	root := t.TempDir()
	// Tree:
	//   keep/film.mkv        (non-empty → preserved, and so is "keep")
	//   empty/               (empty → removed)
	//   nested/a/b/          (all empty → removed bottom-up, incl. "nested")
	//   .thumbs/             (hidden → never touched)
	mustMkdir(t, filepath.Join(root, "keep"))
	mustWrite(t, filepath.Join(root, "keep", "film.mkv"), "v")
	mustMkdir(t, filepath.Join(root, "empty"))
	mustMkdir(t, filepath.Join(root, "nested", "a", "b"))
	mustMkdir(t, filepath.Join(root, ".thumbs"))

	b := NewBrowser([]config.ExternalMount{{Name: "test", Path: root}})

	cleaned, err := b.RemoveEmptyDirs("test", "")
	if err != nil {
		t.Fatalf("RemoveEmptyDirs: %v", err)
	}
	// empty + nested/a/b + nested/a + nested = 4
	if cleaned != 4 {
		t.Fatalf("cleaned = %d, want 4", cleaned)
	}

	assertExists := func(rel string, want bool) {
		t.Helper()
		_, err := os.Stat(filepath.Join(root, rel))
		if want && err != nil {
			t.Fatalf("expected %q to survive: %v", rel, err)
		}
		if !want && err == nil {
			t.Fatalf("expected %q to be removed", rel)
		}
	}
	assertExists("keep", true)          // holds a file
	assertExists("keep/film.mkv", true) // file untouched
	assertExists(".thumbs", true)       // hidden dir never touched
	assertExists("empty", false)        // empty → gone
	assertExists("nested", false)       // emptied bottom-up → gone
	assertExists(".", true)             // mount root never removed
}

func TestRemoveEmptyDirs_NeverRemovesMountRoot(t *testing.T) {
	root := t.TempDir() // completely empty mount
	b := NewBrowser([]config.ExternalMount{{Name: "test", Path: root}})

	cleaned, err := b.RemoveEmptyDirs("test", "")
	if err != nil {
		t.Fatalf("RemoveEmptyDirs: %v", err)
	}
	if cleaned != 0 {
		t.Fatalf("cleaned = %d, want 0 (root must never be removed)", cleaned)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("mount root must survive: %v", err)
	}
}

func TestRemoveEmptyDirs_StartDirPreservedEvenIfEmpty(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "sub", "child"))
	b := NewBrowser([]config.ExternalMount{{Name: "test", Path: root}})

	// Start at "sub": its empty child is removed, but "sub" itself (the start
	// dir the user is browsing) stays.
	cleaned, err := b.RemoveEmptyDirs("test", "sub")
	if err != nil {
		t.Fatalf("RemoveEmptyDirs: %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("cleaned = %d, want 1", cleaned)
	}
	if _, err := os.Stat(filepath.Join(root, "sub")); err != nil {
		t.Fatalf("start dir must survive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sub", "child")); err == nil {
		t.Fatal("empty child should have been removed")
	}
}

func TestRemoveEmptyDirs_NotADir(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "file.txt"), "x")
	b := NewBrowser([]config.ExternalMount{{Name: "test", Path: root}})
	if _, err := b.RemoveEmptyDirs("test", "file.txt"); err == nil {
		t.Fatal("expected error when target is a file, not a directory")
	}
}
