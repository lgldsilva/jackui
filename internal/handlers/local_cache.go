package handlers

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/localcache"
)

// Local file cache endpoints: pre-fetch a whole file from a slow mount (rclone/
// Drive) to local disk so playback is instant, seekable and immune to the
// mount's intermittent I/O errors. The player's "cache" button / mark drives
// these; serving (LocalFile, HLS source) transparently prefers the cached copy.

// LocalCacheStart handles POST /api/local/cache?mount=&path= — enqueues a
// background full-file copy. Read access is enough (it's a read of the source).
func LocalCacheStart(b *local.Browser, cache *localcache.Cache) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount, path, ok := mountPathParams(c)
		if !ok {
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		if cache == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cache local desabilitado"})
			return
		}
		scoped := scopePath(b, c, mount, path)
		abs, err := b.ResolvePath(mount, scoped)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		st, serr := os.Stat(abs)
		if serr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": ErrFileNotFound})
			return
		}
		if st.IsDir() {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrPathIsDir})
			return
		}
		cache.Enqueue(mount, scoped, abs, st.Size())
		c.JSON(http.StatusAccepted, cache.StatusFor(mount, scoped))
	}
}

// LocalCacheStatus handles GET /api/local/cache/status?mount=&path= — the cache
// "mark" the UI polls (none/queued/copying/ready/error + percent).
func LocalCacheStatus(b *local.Browser, cache *localcache.Cache) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount, path, ok := mountPathParams(c)
		if !ok {
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		if cache == nil {
			c.JSON(http.StatusOK, localcache.Snapshot{Status: "none"})
			return
		}
		c.JSON(http.StatusOK, cache.StatusFor(mount, scopePath(b, c, mount, path)))
	}
}

// LocalCacheDelete handles DELETE /api/local/cache?mount=&path= — drops the
// cached copy (the original on the mount is untouched).
func LocalCacheDelete(b *local.Browser, cache *localcache.Cache) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount, path, ok := mountPathParams(c)
		if !ok {
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		if cache != nil {
			cache.Remove(mount, scopePath(b, c, mount, path))
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
		c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPathParam})
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
