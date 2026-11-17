//go:build unix

package localcache

import (
	"os"
	"path/filepath"
	"testing"
)

// Remove used to delete the map entry and the file but leave the copy reading
// the whole remote file into an unlinked inode, blocking the single worker.
func TestRemoveCancelsInFlightCopy(t *testing.T) {
	root := t.TempDir()
	c, _ := newCache(root, 1<<30, nil, false)
	defer c.Close()
	src := newSlowSource(t)

	c.Enqueue("GDrive", "a.mkv", src.path, 1<<20)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.runJob(job{key: key("GDrive", "a.mkv"), srcAbs: src.path})
	}()
	src.feed(t)
	waitCopied(t, c, "GDrive", "a.mkv")
	if _, err := os.Stat(filepath.Join(root, key("GDrive", "a.mkv")+".mkv")); !os.IsNotExist(err) {
		t.Errorf("unfinished copy visible at the final cache path (stat err = %v)", err)
	}

	c.Remove("GDrive", "a.mkv")
	src.feed(t) // the copy notices the cancel on its next chunk

	assertReturns(t, done, "copy after Remove")
	if s := c.StatusFor("GDrive", "a.mkv"); s.Status != "none" {
		t.Errorf("status after Remove = %q, want none", s.Status)
	}
	assertNoCacheFiles(t, root)
}
