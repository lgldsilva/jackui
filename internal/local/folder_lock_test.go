package local

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

// newLockBrowser returns a Browser backed by a real temp dir with a subfolder
// and a regular file, so SetFolderLock can be exercised against the filesystem.
func newLockBrowser(t *testing.T) (*Browser, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	b := NewBrowser([]config.ExternalMount{{Name: "M", Path: root}})
	return b, root
}

func TestSetFolderLock_RoundtripIdempotent(t *testing.T) {
	b, root := newLockBrowser(t)
	marker := filepath.Join(root, "sub", keepMarker)

	// Lock, then lock again (idempotent).
	for i := 0; i < 2; i++ {
		if err := b.SetFolderLock("M", "sub", true); err != nil {
			t.Fatalf("lock (pass %d): %v", i, err)
		}
		if !isFolderLocked(filepath.Join(root, "sub")) {
			t.Fatalf("expected sub locked after pass %d", i)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("expected keep marker after pass %d: %v", i, err)
		}
	}

	// Unlock, then unlock again (idempotent no-op even when marker is gone).
	for i := 0; i < 2; i++ {
		if err := b.SetFolderLock("M", "sub", false); err != nil {
			t.Fatalf("unlock (pass %d): %v", i, err)
		}
		if isFolderLocked(filepath.Join(root, "sub")) {
			t.Fatalf("expected sub unlocked after pass %d", i)
		}
	}
}

func TestSetFolderLock_Errors(t *testing.T) {
	b, _ := newLockBrowser(t)

	cases := []struct {
		name, mount, rel string
	}{
		{"mount root", "M", ""},
		{"mount root dot", "M", "."},
		{"nonexistent path", "M", "missing"},
		{"not a directory", "M", "file.txt"},
		{"unknown mount", "Nope", "sub"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := b.SetFolderLock(tc.mount, tc.rel, true); err == nil {
				t.Fatalf("expected error locking %s/%q", tc.mount, tc.rel)
			}
		})
	}
}
