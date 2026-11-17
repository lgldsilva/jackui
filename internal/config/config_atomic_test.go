package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Resource exhaustion must not reach the in-place fallback: os.WriteFile
// truncates first, so it would trade a failed save for an empty config.yaml.
func TestWriteFileAtomic_ExhaustionNeverFallsBackInPlace(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"rename": func(t *testing.T) {
			orig := renameFile
			renameFile = func(string, string) error { return &os.LinkError{Op: "rename", Err: syscall.ENOSPC} }
			t.Cleanup(func() { renameFile = orig })
		},
		"create temp": func(t *testing.T) {
			orig := createTemp
			createTemp = func(string, string) (*os.File, error) {
				return nil, &os.PathError{Op: "open", Err: syscall.EMFILE}
			}
			t.Cleanup(func() { createTemp = orig })
		},
	}
	for name, inject := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			inject(t)
			if err := writeFileAtomic(path, []byte("new"), 0600); err == nil {
				t.Fatal("want the exhaustion error")
			}
			if got := readFile(t, path); got != "original" {
				t.Errorf("original file was touched: %q", got)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Errorf("temp file left behind: %v", entries)
			}
		})
	}
}

func TestCanWriteInPlace(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.ENOSPC, syscall.EDQUOT, syscall.EMFILE, syscall.ENFILE, syscall.EIO} {
		if canWriteInPlace(fmt.Errorf("wrapped: %w", errno)) {
			t.Errorf("%v must not fall back to an in-place write", errno)
		}
	}
	for _, errno := range []syscall.Errno{syscall.EBUSY, syscall.EACCES, syscall.EROFS, syscall.EXDEV} {
		if !canWriteInPlace(errno) {
			t.Errorf("%v must fall back to an in-place write", errno)
		}
	}
}

// The rename replaces the inode, so the mode an operator gave config.yaml
// (e.g. group-readable for a sidecar) has to be carried over explicitly.
func TestSave_KeepsExistingFileMode(t *testing.T) {
	path := writeConfigFile(t, "port: 1\n")
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	if err := (&Config{Port: 4242}).Save(path); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0640 {
		t.Errorf("mode = %o, want 0640", got)
	}
}

func TestSave_NewFileIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := (&Config{Port: 4242}).Save(path); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0600 {
		t.Errorf("mode = %o, want 0600", got)
	}
}
