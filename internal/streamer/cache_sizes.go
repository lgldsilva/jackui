package streamer

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// sizeCacheTTL bounds how long an idle entry's measured size is trusted.
	// The top-level mtime only reveals changes to the entry's direct children,
	// so a change deeper down (or an untracked writer such as the local-file
	// cache living under DataDir) is picked up at the next re-walk.
	sizeCacheTTL = 5 * time.Minute
	// sizeCacheRacyWindow: an entry modified this recently is not memoized —
	// a later change inside the same filesystem timestamp tick would be
	// indistinguishable from the state that was measured.
	sizeCacheRacyWindow = 2 * time.Second
)

type sizeRecord struct {
	size       int64
	mtime      time.Time // newest mtime found by the walk (LRU ordering)
	topMTime   time.Time // mtime of the entry itself when it was measured
	measuredAt time.Time
}

// entrySizeCache memoizes the disk usage of idle cache entries between
// cache-limit passes. The cap check runs every minute and used to lstat every
// file under DataDir each time, although only torrents being written change.
type entrySizeCache struct {
	mu  sync.Mutex
	m   map[string]sizeRecord
	now func() time.Time // test seam; nil = time.Now
}

func (c *entrySizeCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *entrySizeCache) get(name string, topMTime time.Time) (sizeRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.m[name]
	if !ok || !rec.topMTime.Equal(topMTime) || c.clock().Sub(rec.measuredAt) >= sizeCacheTTL {
		return sizeRecord{}, false
	}
	return rec, true
}

func (c *entrySizeCache) put(name string, rec sizeRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec.measuredAt = c.clock()
	if rec.measuredAt.Sub(rec.topMTime) < sizeCacheRacyWindow {
		delete(c.m, name)
		return
	}
	if c.m == nil {
		c.m = make(map[string]sizeRecord)
	}
	c.m[name] = rec
}

func (c *entrySizeCache) forget(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, name)
}

// retain drops the records of entries no longer on disk.
func (c *entrySizeCache) retain(present map[string]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name := range c.m {
		if _, ok := present[name]; !ok {
			delete(c.m, name)
		}
	}
}

// entrySize measures one top-level cache entry. A volatile entry (active
// torrent or protected download — something is writing into it) is always
// walked and never memoized. reuse lets an idle one be answered from the size
// cache while its own mtime is unchanged.
func (s *Streamer) entrySize(name string, volatile, reuse bool) (int64, time.Time, error) {
	full := filepath.Join(s.cfg.DataDir, name)
	var topMTime time.Time
	// Taken BEFORE the walk: a change racing the walk moves the mtime past
	// this value and invalidates the record.
	if fi, err := os.Lstat(full); err == nil {
		topMTime = fi.ModTime()
	}
	cacheable := !volatile && !topMTime.IsZero()
	if reuse && cacheable {
		if rec, ok := s.sizes.get(name, topMTime); ok {
			return rec.size, rec.mtime, nil
		}
	}
	measure := s.measureEntry
	if measure == nil {
		measure = dirSizeAndMTime
	}
	size, mtime, err := measure(full)
	if err != nil || !cacheable {
		s.sizes.forget(name)
		return size, mtime, err
	}
	s.sizes.put(name, sizeRecord{size: size, mtime: mtime, topMTime: topMTime})
	return size, mtime, nil
}

// favoriteLookup resolves the favorite flag for a whole Stats pass with ONE
// query instead of a COUNT per entry (favorites has no index led by name).
// Fail-closed like IsFavorite: when the names cannot be loaded every entry
// counts as a favorite, so eviction defers all of them to the last tier.
func (s *Streamer) favoriteLookup() func(name string) bool {
	load := s.favoriteNames
	if load == nil {
		if s.favs == nil {
			return func(string) bool { return false }
		}
		load = s.favs.Names
	}
	names, err := load()
	if err != nil {
		return func(string) bool { return true }
	}
	return func(name string) bool { return names[name] }
}

// dirSizeAndMTime returns the *physical* bytes allocated on disk under a path
// (file or dir), plus the newest mtime.
//
// Why physical (not logical): anacrolix writes sparse files — it opens the
// target file and writes only the bytes for completed pieces. The file's
// logical size (info.Size()) is the **final torrent size**, but the actual
// blocks consumed on disk grow progressively as pieces arrive.
//
// For cache eviction and the UI "X / Y used" indicator, what the user cares
// about is the *real* footprint, not the logical placeholder. Reporting
// logical size makes a 10 GB torrent look "fully cached" the moment metadata
// is received, which is why the cache UI looked pre-allocated.
//
// We use the platform-specific allocated-block count when available (POSIX
// stat.Blocks * 512) and fall back to logical size on platforms where the
// syscall data isn't accessible.
func dirSizeAndMTime(path string) (int64, time.Time, error) {
	var size int64
	var mtime time.Time
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += physicalBytes(info)
		}
		if info.ModTime().After(mtime) {
			mtime = info.ModTime()
		}
		return nil
	})
	return size, mtime, err
}
