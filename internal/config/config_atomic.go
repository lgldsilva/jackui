package config

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// writeFileAtomic replaces path with data through a synced temp file in the
// same directory plus a rename, so a reader or a crash never observes a
// truncated config. A failure while WRITING the temp file (disk full, I/O
// error) is returned with the original file untouched.
//
// When the swap itself is impossible — the directory is not writable, or path
// is a single-file bind mount, which cannot be renamed over (EBUSY; the stock
// docker-compose mounts config.yaml that way) — it falls back to writing in
// place, which is not atomic. Resource exhaustion never takes that fallback:
// the in-place write truncates first, so it would only empty the file.
//
// An existing file keeps its mode; the rename replaces the inode, so owner and
// group become the server's own.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target // keep a symlinked config a symlink
	}
	// A rename only needs a writable DIRECTORY; honour a read-only (or
	// foreign-owned) config.yaml the way the in-place write always did.
	if err := CheckWritable(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}
	tmp, err := createTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return writeInPlace(path, data, perm, err)
	}
	if err := fillTempFile(tmp, data, perm); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := renameFile(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return writeInPlace(path, data, perm, err)
	}
	return nil
}

func writeInPlace(path string, data []byte, perm os.FileMode, cause error) error {
	if !canWriteInPlace(cause) {
		return cause
	}
	return os.WriteFile(path, data, perm)
}

// canWriteInPlace reports whether cause means "the swap is impossible here"
// (read-only directory, bind-mounted file) rather than "the system is out of
// space, quota, descriptors or a healthy disk".
func canWriteInPlace(cause error) bool {
	for _, errno := range []syscall.Errno{syscall.ENOSPC, syscall.EDQUOT, syscall.EMFILE, syscall.ENFILE, syscall.EIO} {
		if errors.Is(cause, errno) {
			return false
		}
	}
	return true
}

// Variables so tests can simulate a full disk and a single-file bind mount.
var (
	createTemp   = os.CreateTemp
	fillTempFile = fillTemp
	renameFile   = os.Rename
)

func fillTemp(f *os.File, data []byte, perm os.FileMode) error {
	_, err := f.Write(data)
	if err == nil {
		err = f.Chmod(perm)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
