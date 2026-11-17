package localcache

import (
	"os"
	"path/filepath"
	"testing"
)

// A file bigger than the whole cache used to be copied in full and then
// evicted the moment it turned ready.
func TestEnqueueRejectsFileLargerThanCache(t *testing.T) {
	src := t.TempDir()
	c, _ := newCache(t.TempDir(), 1000, nil, false)
	defer c.Close()
	p, size := writeSrc(t, src, "huge.mkv", 2000)

	c.Enqueue("GDrive", "huge.mkv", p, size)

	s := c.StatusFor("GDrive", "huge.mkv")
	if s.Status != string(StatusError) || s.Error != errTooLarge {
		t.Fatalf("status = %+v, want error %q", s, errTooLarge)
	}
	if len(c.jobs) != 0 {
		t.Error("an oversized file must not be queued for copying")
	}
}

func TestStalePartialsSweptOnOpen(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "deadbeef.mkv"+partialSuffix)
	keepDir := filepath.Join(root, "subs"+partialSuffix)
	if err := os.WriteFile(stale, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(keepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	c, _ := newCache(root, 1<<30, nil, false)
	defer c.Close()

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale partial survived reopen (stat err = %v)", err)
	}
	if _, err := os.Stat(keepDir); err != nil {
		t.Errorf("directories must be left alone: %v", err)
	}
}

func TestSweepPartialsToleratesMissingRoot(t *testing.T) {
	c, _ := newCache(t.TempDir(), 1<<30, nil, false)
	defer c.Close()
	c.root = filepath.Join(c.root, "gone")
	c.sweepPartials() // must not panic
}

// A job left in the queue by Remove must not copy again on behalf of an entry
// that is not waiting for it.
func TestRunJobSkipsEntryThatIsNotQueued(t *testing.T) {
	src := t.TempDir()
	c, _ := newCache(t.TempDir(), 1<<30, nil, false)
	defer c.Close()
	p, size := writeSrc(t, src, "a.mkv", 100)
	cacheAndRun(c, "GDrive", "a.mkv", p, size)
	cp, ok := c.CachedPath("GDrive", "a.mkv")
	if !ok {
		t.Fatal("expected ready entry")
	}
	if err := os.WriteFile(cp, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}

	c.runJob(job{key: key("GDrive", "a.mkv"), srcAbs: p}) // stale duplicate job

	got, _ := os.ReadFile(cp)
	if string(got) != "sentinel" {
		t.Error("a ready entry was copied again by a stale job")
	}
}

func TestRenameFailureMarksEntryAndCleansPartial(t *testing.T) {
	src := t.TempDir()
	root := t.TempDir()
	c, _ := newCache(root, 1<<30, nil, false)
	defer c.Close()
	p, size := writeSrc(t, src, "a.mkv", 100)
	c.Enqueue("GDrive", "a.mkv", p, size)
	// A non-empty directory at the final path makes the rename fail.
	final := filepath.Join(root, key("GDrive", "a.mkv")+".mkv")
	if err := os.MkdirAll(filepath.Join(final, "x"), 0o755); err != nil {
		t.Fatal(err)
	}

	c.runJob(job{key: key("GDrive", "a.mkv"), srcAbs: p})

	if s := c.StatusFor("GDrive", "a.mkv"); s.Status != string(StatusError) || s.Error == "" {
		t.Fatalf("status = %+v, want error", s)
	}
	if _, err := os.Stat(final + partialSuffix); !os.IsNotExist(err) {
		t.Errorf("partial left behind (stat err = %v)", err)
	}
}

// An entry removed between the last chunk and the ready flip must not be
// resurrected, and a re-enqueued entry under the same key must stay untouched.
func TestMarkReadyRejectsDetachedEntry(t *testing.T) {
	src := t.TempDir()
	c, _ := newCache(t.TempDir(), 1<<30, nil, false)
	defer c.Close()
	p, size := writeSrc(t, src, "a.mkv", 100)
	k := key("GDrive", "a.mkv")
	c.Enqueue("GDrive", "a.mkv", p, size)
	old, _, cancel := c.beginCopy(k)
	defer cancel()
	c.Remove("GDrive", "a.mkv")
	c.Enqueue("GDrive", "a.mkv", p, size)

	if c.markReady(k, old) {
		t.Fatal("detached entry was published as ready")
	}
	if s := c.StatusFor("GDrive", "a.mkv"); s.Status != string(StatusQueued) {
		t.Errorf("re-enqueued entry status = %q, want queued", s.Status)
	}
}
