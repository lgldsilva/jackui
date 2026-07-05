// Package localcache pre-fetches whole files from slow/remote mounts (rclone /
// Google Drive) onto fast local disk so playback is instant, seekable, and
// immune to the intermittent I/O errors the FUSE mount throws. Unlike rclone's
// own VFS cache — which only fills as bytes are read, so the FIRST play still
// pays the cold-start latency — this downloads the ENTIRE file up front when
// the user marks it, then serves it locally. The cache is size-capped with LRU
// eviction (like the torrent piece cache), so it never fills the disk.
package localcache

import (
	// #nosec G505 -- import de sha1 p/ hash de conteudo (dedup/oshash), nao cripto de seguranca
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Status string

const (
	StatusQueued  Status = "queued"
	StatusCopying Status = "copying"
	StatusReady   Status = "ready"
	StatusError   Status = "error"
)

const copyChunk = 4 << 20 // 4 MiB copy buffer (large reads = fewer rclone round-trips)

// Snapshot is the JSON-friendly view returned to the UI (the "cache mark").
type Snapshot struct {
	Status  string `json:"status"` // "none" when there's no entry
	Size    int64  `json:"size"`
	Copied  int64  `json:"copied"`
	Percent int    `json:"percent"`
	Error   string `json:"error,omitempty"`
}

type entry struct {
	Mount      string    `json:"mount"`
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	Copied     int64     `json:"-"`
	Status     Status    `json:"-"`
	Err        string    `json:"-"`
	CachePath  string    `json:"cachePath"`
	LastAccess time.Time `json:"lastAccess"`
}

type job struct {
	key, srcAbs string
}

// Cache stores fully-copied files under root, capped at maxBytes (LRU eviction).
type Cache struct {
	root     string
	maxBytes int64
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
	jobs    chan job
	stop    chan struct{}
}

// New creates (or reopens) a cache rooted at root, capped at maxGB gigabytes
// (≤0 → 50 GiB default), and starts the copy worker. Existing ready files are
// reloaded from the on-disk index.
func New(root string, maxGB int) (*Cache, error) {
	if maxGB <= 0 {
		maxGB = 50
	}
	return newCache(root, int64(maxGB)<<30, time.Now, true)
}

func newCache(root string, maxBytes int64, nowFn func() time.Time, runWorker bool) (*Cache, error) {
	if maxBytes <= 0 {
		maxBytes = 50 << 30
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	c := &Cache{
		root:     root,
		maxBytes: maxBytes,
		now:      nowFn,
		entries:  make(map[string]*entry),
		jobs:     make(chan job, 64),
		stop:     make(chan struct{}),
	}
	c.loadIndex()
	if runWorker {
		go c.worker()
	}
	return c, nil
}

func key(mount, path string) string {
	// #nosec G401 -- sha1/md5 p/ hash de conteudo (dedup/oshash), nao uso criptografico de seguranca
	sum := sha1.Sum([]byte(mount + "|" + path))
	return hex.EncodeToString(sum[:])
}

// Enqueue marks (mount, path) for caching: copies srcAbs (the resolved on-disk
// path on the slow mount) to local disk in the background. A no-op if the file
// is already cached or in flight.
func (c *Cache) Enqueue(mount, path, srcAbs string, size int64) {
	k := key(mount, path)
	c.mu.Lock()
	if e, ok := c.entries[k]; ok && (e.Status == StatusReady || e.Status == StatusCopying || e.Status == StatusQueued) {
		c.mu.Unlock()
		return
	}
	c.entries[k] = &entry{
		Mount: mount, Path: path, Size: size, Status: StatusQueued,
		CachePath:  filepath.Join(c.root, k+filepath.Ext(path)),
		LastAccess: c.now(),
	}
	c.mu.Unlock()
	select {
	case c.jobs <- job{key: k, srcAbs: srcAbs}:
	default:
		// Queue full — mark error so the UI doesn't spin forever.
		c.setStatus(k, StatusError, "fila de cache cheia")
	}
}

// Root returns the cache's on-disk root directory. Callers use it to derive
// sibling caches (e.g. an extracted-subtitle VTT cache) that share the same
// fast-disk location without coupling to this package's internals.
func (c *Cache) Root() string { return c.root }

// CachedPath returns the local cached path for (mount, path) when ready, and
// bumps its LRU recency so an actively-watched file isn't evicted.
func (c *Cache) CachedPath(mount, path string) (string, bool) {
	k := key(mount, path)
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok || e.Status != StatusReady {
		return "", false
	}
	e.LastAccess = c.now()
	return e.CachePath, true
}

// StatusFor returns the cache mark for (mount, path); Status "none" if absent.
func (c *Cache) StatusFor(mount, path string) Snapshot {
	k := key(mount, path)
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok {
		return Snapshot{Status: "none"}
	}
	return snapshotOf(e)
}

// Remove drops a cached/queued entry and deletes its file.
func (c *Cache) Remove(mount, path string) {
	k := key(mount, path)
	c.mu.Lock()
	e, ok := c.entries[k]
	if ok {
		delete(c.entries, k)
	}
	c.mu.Unlock()
	if ok {
		_ = os.Remove(e.CachePath)
		c.saveIndex()
	}
}

func snapshotOf(e *entry) Snapshot {
	pct := 0
	if e.Size > 0 {
		pct = int(e.Copied * 100 / e.Size)
		if pct > 100 {
			pct = 100
		}
	} else if e.Status == StatusReady {
		pct = 100
	}
	return Snapshot{Status: string(e.Status), Size: e.Size, Copied: e.Copied, Percent: pct, Error: e.Err}
}

func (c *Cache) worker() {
	for {
		select {
		case <-c.stop:
			return
		case j := <-c.jobs:
			c.runJob(j)
		}
	}
}

// runJob copies the source file to its cache path, reporting progress, then
// marks it ready and evicts down to the size cap. Errors are recorded on the
// entry (the UI surfaces them) rather than panicking.
func (c *Cache) runJob(j job) {
	c.mu.Lock()
	e, ok := c.entries[j.key]
	c.mu.Unlock()
	if !ok {
		return
	}
	c.setStatus(j.key, StatusCopying, "")
	if err := c.copyFile(j.key, j.srcAbs, e.CachePath); err != nil {
		_ = os.Remove(e.CachePath)
		c.setStatus(j.key, StatusError, err.Error())
		return
	}
	c.mu.Lock()
	e.Status = StatusReady
	e.LastAccess = c.now()
	c.mu.Unlock()
	c.evict()
	c.saveIndex()
}

func (c *Cache) copyFile(k, srcAbs, dst string) error {
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
	in, err := os.Open(srcAbs)
	if err != nil {
		return err
	}
	defer in.Close()
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	buf := make([]byte, copyChunk)
	var copied int64
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			copied += int64(n)
			c.mu.Lock()
			if e, ok := c.entries[k]; ok {
				e.Copied = copied
			}
			c.mu.Unlock()
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return out.Close()
}

func (c *Cache) setStatus(k string, s Status, errMsg string) {
	c.mu.Lock()
	if e, ok := c.entries[k]; ok {
		e.Status = s
		e.Err = errMsg
	}
	c.mu.Unlock()
}

// evict removes least-recently-accessed ready entries until the total cached
// size is under the cap. Called with the lock NOT held.
func (c *Cache) evict() {
	c.mu.Lock()
	defer c.mu.Unlock()
	var total int64
	ready := make([]*entry, 0, len(c.entries))
	for _, e := range c.entries {
		if e.Status == StatusReady {
			total += e.Size
			ready = append(ready, e)
		}
	}
	if total <= c.maxBytes {
		return
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].LastAccess.Before(ready[j].LastAccess) })
	for _, e := range ready {
		if total <= c.maxBytes {
			break
		}
		_ = os.Remove(e.CachePath)
		delete(c.entries, key(e.Mount, e.Path))
		total -= e.Size
	}
}

// Close stops the worker.
func (c *Cache) Close() {
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
}

// ── On-disk index (so ready files survive a restart) ───────────────────────

func (c *Cache) indexPath() string { return filepath.Join(c.root, "index.json") }

func (c *Cache) loadIndex() {
	data, err := os.ReadFile(c.indexPath())
	if err != nil {
		return
	}
	var saved []entry
	if json.Unmarshal(data, &saved) != nil {
		return
	}
	for i := range saved {
		e := saved[i]
		// Only trust entries whose file still exists with the recorded size.
		st, serr := os.Stat(e.CachePath)
		if serr != nil || st.Size() != e.Size {
			_ = os.Remove(e.CachePath)
			continue
		}
		e.Status = StatusReady
		e.Copied = e.Size
		c.entries[key(e.Mount, e.Path)] = &e
	}
}

func (c *Cache) saveIndex() {
	c.mu.Lock()
	ready := make([]entry, 0, len(c.entries))
	for _, e := range c.entries {
		if e.Status == StatusReady {
			ready = append(ready, *e)
		}
	}
	c.mu.Unlock()
	data, err := json.Marshal(ready)
	if err != nil {
		return
	}
	tmp := c.indexPath() + ".tmp"
	// #nosec G306 -- arquivo de midia/cache; 0644 intencional p/ leitura
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, c.indexPath())
	}
}
