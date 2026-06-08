package localcache

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }
func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// writeSrc writes a source file (simulating a file on the slow mount) and
// returns its abs path + size.
func writeSrc(t *testing.T, dir, name string, n int) (string, int64) {
	t.Helper()
	p := filepath.Join(dir, name)
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i % 251)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	return p, int64(n)
}

// cacheAndRun enqueues then synchronously runs the copy (bypassing the async
// worker channel) so the test is deterministic.
func cacheAndRun(c *Cache, mount, path, srcAbs string, size int64) {
	c.Enqueue(mount, path, srcAbs, size)
	c.runJob(job{key: key(mount, path), srcAbs: srcAbs})
}

func TestEnqueueCopiesAndBecomesReady(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	c, _ := newCache(root, 1<<30, nil, false)
	defer c.Close()

	p, size := writeSrc(t, src, "movie.mkv", 9000)
	cacheAndRun(c, "GDrive", "movies/movie.mkv", p, size)

	cp, ok := c.CachedPath("GDrive", "movies/movie.mkv")
	if !ok {
		t.Fatal("expected cached path ready")
	}
	got, _ := os.ReadFile(cp)
	orig, _ := os.ReadFile(p)
	if len(got) != len(orig) || string(got) != string(orig) {
		t.Fatal("cached content mismatch")
	}
	if snap := c.StatusFor("GDrive", "movies/movie.mkv"); snap.Status != "ready" || snap.Percent != 100 {
		t.Fatalf("status=%+v want ready/100%%", snap)
	}
	// Cached file keeps the original extension (helps ffprobe/Content-Type).
	if filepath.Ext(cp) != ".mkv" {
		t.Fatalf("cache path ext = %q want .mkv", filepath.Ext(cp))
	}
}

func TestStatusForNoneWhenAbsent(t *testing.T) {
	c, _ := newCache(t.TempDir(), 1<<30, nil, false)
	defer c.Close()
	if snap := c.StatusFor("M", "x"); snap.Status != "none" {
		t.Fatalf("status=%q want none", snap.Status)
	}
}

func TestEnqueueDedups(t *testing.T) {
	root, src := t.TempDir(), t.TempDir()
	c, _ := newCache(root, 1<<30, nil, false)
	defer c.Close()
	p, size := writeSrc(t, src, "a.mkv", 100)
	cacheAndRun(c, "M", "a.mkv", p, size)
	// Second enqueue while ready is a no-op (no duplicate, stays ready).
	c.Enqueue("M", "a.mkv", p, size)
	if snap := c.StatusFor("M", "a.mkv"); snap.Status != "ready" {
		t.Fatalf("status=%q want ready", snap.Status)
	}
}

func TestRemoveDeletesFile(t *testing.T) {
	root, src := t.TempDir(), t.TempDir()
	c, _ := newCache(root, 1<<30, nil, false)
	defer c.Close()
	p, size := writeSrc(t, src, "a.mkv", 100)
	cacheAndRun(c, "M", "a.mkv", p, size)
	cp, _ := c.CachedPath("M", "a.mkv")
	c.Remove("M", "a.mkv")
	if _, err := os.Stat(cp); !os.IsNotExist(err) {
		t.Fatal("cache file should be deleted")
	}
	if snap := c.StatusFor("M", "a.mkv"); snap.Status != "none" {
		t.Fatalf("status=%q want none after remove", snap.Status)
	}
}

func TestEvictionRemovesLeastRecentlyUsed(t *testing.T) {
	root, src := t.TempDir(), t.TempDir()
	clk := newClock()
	// Cap = 2500 bytes; each file is 1000 → only 2 fit.
	c, _ := newCache(root, 2500, clk.now, false)
	defer c.Close()

	pa, sa := writeSrc(t, src, "a.mkv", 1000)
	cacheAndRun(c, "M", "a.mkv", pa, sa)
	clk.advance(time.Second)
	pb, sb := writeSrc(t, src, "b.mkv", 1000)
	cacheAndRun(c, "M", "b.mkv", pb, sb)

	// Touch A so B becomes the least-recently-used.
	clk.advance(time.Second)
	c.CachedPath("M", "a.mkv")

	clk.advance(time.Second)
	pc, sc := writeSrc(t, src, "c.mkv", 1000)
	cacheAndRun(c, "M", "c.mkv", pc, sc) // now 3000 > 2500 → evict LRU (B)

	if _, ok := c.CachedPath("M", "b.mkv"); ok {
		t.Fatal("B should have been evicted (least recently used)")
	}
	if _, ok := c.CachedPath("M", "a.mkv"); !ok {
		t.Fatal("A was recently touched, must survive")
	}
	if _, ok := c.CachedPath("M", "c.mkv"); !ok {
		t.Fatal("C is newest, must survive")
	}
}

func TestIndexPersistsReadyAcrossReopen(t *testing.T) {
	root, src := t.TempDir(), t.TempDir()
	c, _ := newCache(root, 1<<30, nil, false)
	p, size := writeSrc(t, src, "a.mkv", 500)
	cacheAndRun(c, "M", "a.mkv", p, size)
	c.Close()

	// Reopen the same root: the ready entry should be reloaded from index.json.
	c2, _ := newCache(root, 1<<30, nil, false)
	defer c2.Close()
	if _, ok := c2.CachedPath("M", "a.mkv"); !ok {
		t.Fatal("ready entry should survive a reopen")
	}
}

func TestRealWorkerCopiesAsync(t *testing.T) {
	root, src := t.TempDir(), t.TempDir()
	c, err := New(root, 1) // real worker goroutine
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	p, size := writeSrc(t, src, "a.mkv", 4096)
	c.Enqueue("M", "a.mkv", p, size)

	// Poll until the worker finishes the copy.
	ready := false
	for i := 0; i < 100; i++ {
		if c.StatusFor("M", "a.mkv").Status == "ready" {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("worker did not mark the file ready in time")
	}
	if _, ok := c.CachedPath("M", "a.mkv"); !ok {
		t.Fatal("expected cached path after worker ran")
	}
	c.Close() // idempotent second close below
	c.Close()
}

func TestCopyErrorMarksEntry(t *testing.T) {
	c, _ := newCache(t.TempDir(), 1<<30, nil, false)
	defer c.Close()
	// Enqueue with a non-existent source → copy fails → status error.
	c.Enqueue("M", "missing.mkv", "/no/such/file", 10)
	c.runJob(job{key: key("M", "missing.mkv"), srcAbs: "/no/such/file"})
	if snap := c.StatusFor("M", "missing.mkv"); snap.Status != "error" {
		t.Fatalf("status=%q want error", snap.Status)
	}
}
