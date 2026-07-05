package local

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/localcache"
)

// Local file cache endpoints: pre-fetch a whole file from a slow mount (rclone/
// Drive) to local disk so playback is instant, seekable and immune to the
// mount's intermittent I/O errors. The player's "cache" button / mark drives
// these; serving (LocalFile, HLS source) transparently prefers the cached copy.

// isRemoteFS reports whether a path lives on a slow/remote mount (rclone, NFS,
// CIFS) worth caching. Indirected through a var so tests can force either side
// without a real FUSE/NAS mount.
var isRemoteFS = detectRemoteFS

// DetectRemoteFS reports whether abs lives on a remote/FUSE mount (rclone, NFS,
// CIFS/SMB). Exported wrapper for cross-package use — cross-torrent dedup (#23)
// flags cloud candidates so they're matched by cheap fingerprint, never a full
// piece read. Delegates to isRemoteFS so test overrides are honoured too.
func DetectRemoteFS(abs string) bool { return isRemoteFS(abs) }

// cacheStatusResponse is the cache "mark" plus a flag telling the UI whether
// caching even makes sense here. Files already on a local disk return
// cacheable=false, so the player hides the cache button entirely — there's
// nothing to pre-fetch (they're already fast and seekable).
type cacheStatusResponse struct {
	localcache.Snapshot
	Cacheable bool `json:"cacheable"`
}

// mountCacheable is true only when the cache is enabled AND the resolved file
// sits on a remote/FUSE mount. A nil cache or a local-disk file → false.
func mountCacheable(b *lb.Browser, cache *localcache.Cache, mount, scoped string) bool {
	if cache == nil {
		return false
	}
	abs, err := b.ResolvePath(mount, scoped)
	if err != nil {
		return false
	}
	return isRemoteFS(abs)
}

// LocalCacheStart handles POST /api/local/cache?mount=&path= — enqueues a
// background full-file copy. Read access is enough (it's a read of the source).
func LocalCacheStart(b *lb.Browser, cache *localcache.Cache) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount, path, ok := mountPathParams(c)
		if !ok {
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}
		if cache == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cache local desabilitado"})
			return
		}
		scoped := ScopePath(b, c, mount, path)
		abs, err := b.ResolvePath(mount, scoped)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		st, serr := os.Stat(abs)
		if serr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": httpshared.ErrFileNotFound})
			return
		}
		if st.IsDir() {
			c.JSON(http.StatusBadRequest, gin.H{"error": httpshared.ErrPathIsDir})
			return
		}
		cache.Enqueue(mount, scoped, abs, st.Size())
		c.JSON(http.StatusAccepted, cacheStatusResponse{
			Snapshot:  cache.StatusFor(mount, scoped),
			Cacheable: isRemoteFS(abs),
		})
	}
}

// LocalCacheFolder handles POST /api/local/cache/folder?mount=&path= — enqueues
// a background full-file copy for EVERY playable file under the folder
// (recursive). One click pre-fetches a whole rclone/Drive series folder to local
// disk instead of caching file by file. A big folder won't overrun the cache:
// the LRU drops the coldest cached files as new copies land (favourites/active
// downloads stay protected); the enqueue just lines them up.
func LocalCacheFolder(b *lb.Browser, cache *localcache.Cache) gin.HandlerFunc {
	return func(c *gin.Context) { localCacheFolderHandler(b, cache, c) }
}

func localCacheFolderHandler(b *lb.Browser, cache *localcache.Cache, c *gin.Context) {
	mount := c.Query("mount")
	if mount == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
		return
	}
	path := c.Query("path") // empty = mount root
	if !CheckMountAccess(b, c, mount) {
		return
	}
	if cache == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cache local desabilitado"})
		return
	}
	scoped := ScopePath(b, c, mount, path)
	if !mountCacheable(b, cache, mount, scoped) {
		// Local-disk mount: nothing to pre-fetch (already fast/seekable).
		c.JSON(http.StatusOK, gin.H{"queued": 0, "cacheable": false})
		return
	}
	entries, err := b.Walk(mount, scoped, true) // mediaOnly
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	queued := enqueueCacheEntries(cache, b, mount, entries)
	c.JSON(http.StatusAccepted, gin.H{"queued": queued, "cacheable": true})
}

func enqueueCacheEntries(cache *localcache.Cache, b *lb.Browser, mount string, entries []lb.Entry) int {
	queued := 0
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		abs, rerr := b.ResolvePath(mount, e.Path)
		if rerr != nil {
			continue
		}
		cache.Enqueue(mount, e.Path, abs, e.Size)
		queued++
	}
	return queued
}

// LocalCacheStatus handles GET /api/local/cache/status?mount=&path= — the cache
// "mark" the UI polls (none/queued/copying/ready/error + percent).
func LocalCacheStatus(b *lb.Browser, cache *localcache.Cache) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount, path, ok := mountPathParams(c)
		if !ok {
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}
		scoped := ScopePath(b, c, mount, path)
		snap := localcache.Snapshot{Status: "none"}
		if cache != nil {
			snap = cache.StatusFor(mount, scoped)
		}
		c.JSON(http.StatusOK, cacheStatusResponse{
			Snapshot:  snap,
			Cacheable: mountCacheable(b, cache, mount, scoped),
		})
	}
}

// LocalCacheDelete handles DELETE /api/local/cache?mount=&path= — drops the
// cached copy (the original on the mount is untouched).
func LocalCacheDelete(b *lb.Browser, cache *localcache.Cache) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount, path, ok := mountPathParams(c)
		if !ok {
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}
		if cache != nil {
			cache.Remove(mount, ScopePath(b, c, mount, path))
		}
		c.JSON(http.StatusOK, gin.H{"removed": true})
	}
}

// mountPathParams reads the shared mount/path query params, writing a 400 and
// returning ok=false when either is missing.
func mountPathParams(c *gin.Context) (string, string, bool) {
	mount := c.Query("mount")
	path := c.Query("path")
	if mount == "" || path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
		return "", "", false
	}
	return mount, path, true
}

// cachedAbs returns the local cached path for (mount, scoped) when ready,
// otherwise the given fallback (the original on-disk path). Lets the serving
// paths transparently read from the fast local copy.
func cachedAbs(cache *localcache.Cache, mount, scoped, fallback string) string {
	if cache == nil {
		return fallback
	}
	if cp, ok := cache.CachedPath(mount, scoped); ok {
		return cp
	}
	return fallback
}

type cachedFile struct {
	abs  string
	stat os.FileInfo
}

// cacheReady returns the cached file (path + stat) for (mount, scoped) when it
// is fully cached and still on disk. Used by the HLS path so probe + ffmpeg
// read the local copy.
func cacheReady(cache *localcache.Cache, mount, scoped string) (cachedFile, bool) {
	if cache == nil {
		return cachedFile{}, false
	}
	cp, ok := cache.CachedPath(mount, scoped)
	if !ok {
		return cachedFile{}, false
	}
	st, err := os.Stat(cp)
	if err != nil {
		return cachedFile{}, false
	}
	return cachedFile{abs: cp, stat: st}, true
}
