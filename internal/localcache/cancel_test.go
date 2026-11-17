//go:build unix

package localcache

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// slowSource is a FIFO standing in for a file on a slow remote mount: the copy
// only advances when the test feeds it, so a test can act mid-copy.
type slowSource struct {
	path string
	w    *os.File
}

func newSlowSource(t *testing.T) *slowSource {
	t.Helper()
	p := filepath.Join(t.TempDir(), "remote.mkv")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	// O_RDWR never blocks on a FIFO and keeps it open until the test closes it,
	// so the copy sees neither EOF nor a missing writer on its own.
	w, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open fifo: %v", err)
	}
	s := &slowSource{path: p, w: w}
	t.Cleanup(func() { _ = w.Close() })
	return s
}

func (s *slowSource) feed(t *testing.T) {
	t.Helper()
	if _, err := s.w.Write(make([]byte, 1024)); err != nil {
		t.Fatalf("feed: %v", err)
	}
}

func waitCopied(t *testing.T, c *Cache, mount, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.StatusFor(mount, path).Copied == 0 {
		if time.Now().After(deadline) {
			t.Fatal("copy never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertReturns(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not return while the source still had data to deliver", what)
	}
}

func assertNoCacheFiles(t *testing.T, root string) {
	t.Helper()
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range ents {
		if e.Name() != "index.json" {
			t.Errorf("leftover cache file %q", e.Name())
		}
	}
}

// Close used to wait for a multi-GB copy to finish, eating the process-wide
// cleanup deadline during shutdown.
func TestCloseAbortsInFlightCopy(t *testing.T) {
	root := t.TempDir()
	c, _ := newCache(root, 1<<30, nil, true)
	src := newSlowSource(t)

	c.Enqueue("GDrive", "a.mkv", src.path, 1<<20)
	src.feed(t)
	waitCopied(t, c, "GDrive", "a.mkv")

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Close()
	}()
	// Keep feeding: Close may land before or after the worker reads a chunk.
	stopFeed := make(chan struct{})
	defer close(stopFeed)
	go func() {
		for {
			select {
			case <-stopFeed:
				return
			case <-time.After(20 * time.Millisecond):
				_, _ = src.w.Write(make([]byte, 1024))
			}
		}
	}()

	assertReturns(t, done, "Close")
	assertNoCacheFiles(t, root)
}
