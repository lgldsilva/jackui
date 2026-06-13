package handlers

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/audiometa"
	"github.com/lgldsilva/jackui/internal/imagesearch"
	"github.com/lgldsilva/jackui/internal/local"
)

// resolveLocalAudio resolves an audio request to its absolute on-disk path,
// applying the SAME access control as every other local route (mount ACL +
// per-user scoping). This is what keeps the tag cache safe despite being keyed
// by absolute path: a user can only ever resolve paths they're allowed to see
// (UserSubpath mounts embed the username; AllowedUsers gate visibility). Writes
// the HTTP error and returns ok=false on any failure.
func resolveLocalAudio(b *local.Browser, c *gin.Context) (abs string, stat os.FileInfo, ok bool) {
	mount := c.Query("mount")
	path := c.Query("path")
	if mount == "" || path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPathParam})
		return "", nil, false
	}
	if !checkMountAccess(b, c, mount) {
		return "", nil, false
	}
	if !isAudioByExt(path) {
		c.Status(http.StatusNoContent)
		return "", nil, false
	}
	resolved, err := resolveLocalAbs(b, mount, scopePath(b, c, mount, path))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return "", nil, false
	}
	if resolved == "" {
		c.Status(http.StatusNotFound)
		return "", nil, false
	}
	st, err := os.Stat(resolved)
	if err != nil {
		c.Status(http.StatusNotFound)
		return "", nil, false
	}
	return resolved, st, true
}

// audioETag derives a strong-ish validator from the path + modtime so a re-rip
// or promote (which changes mtime) invalidates a cached cover/tags in the
// browser, even though the cover endpoint advertises a long max-age.
func audioETag(abs string, modUnix int64) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d", abs, modUnix)))
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// LocalAudioMeta handles GET /api/local/audio/meta?mount&path — returns the
// file's tags (title/artist/album/…). Cached in .audio-metadata.db keyed by
// (abs path, mtime); a stale/missing row is re-parsed and saved. A parse error
// (corrupt/unsupported file) is non-fatal: returns empty tags so the UI just
// falls back to the filename.
func LocalAudioMeta(b *local.Browser, store *audiometa.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		abs, stat, ok := resolveLocalAudio(b, c)
		if !ok {
			return
		}
		modUnix := stat.ModTime().Unix()
		if t, hit := store.Get(abs, modUnix); hit {
			c.JSON(http.StatusOK, t)
			return
		}
		t, err := audiometa.ReadTags(abs)
		if err != nil {
			// Unsupported/corrupt — surface empty tags, don't 500.
			c.JSON(http.StatusOK, audiometa.Tags{})
			return
		}
		_ = store.Save(abs, modUnix, t)
		c.JSON(http.StatusOK, t)
	}
}

// LocalAudioCover handles GET /api/local/audio/cover?mount&path — serves the
// embedded album art. 204 when the file has no embedded picture (the client
// then falls back to the per-title TMDB poster, like the cards do). Long-cached
// with an mtime-based ETag so a changed file busts the browser cache.
//
// Served to <img>, which can't send an Authorization header → the route is in
// the isMediaPath whitelist (auth/middleware.go) and accepts ?token=.
func LocalAudioCover(b *local.Browser, store *audiometa.Store, webSearch *imagesearch.Chain) gin.HandlerFunc {
	return func(c *gin.Context) {
		abs, stat, ok := resolveLocalAudio(b, c)
		if !ok {
			return
		}
		modUnix := stat.ModTime().Unix()
		etag := audioETag(abs, modUnix)
		if match := c.GetHeader("If-None-Match"); match != "" && strings.Contains(match, etag) {
			c.Status(http.StatusNotModified)
			return
		}
		// Embedded cover first. A cached row saying "no embedded cover" skips the
		// re-parse and jumps straight to the web fallback.
		if t, hit := store.Get(abs, modUnix); !(hit && !t.HasCover) {
			if cover, has, err := audiometa.ReadCover(abs); err == nil && has {
				c.Header(CacheControl, CachePublicYear)
				c.Header("ETag", etag)
				c.Data(http.StatusOK, cover.MIMEType, cover.Data)
				return
			}
		}
		// No embedded picture → search the web with the SAME chain the cards use
		// (DuckDuckGo→Bing, keyless), querying by the file's tags. Browser caches
		// the result via the long max-age + ETag, so the search fires once.
		serveWebCover(c, abs, etag, webSearch)
	}
}

// serveWebCover looks up album art on the web for a file with no embedded
// picture and serves the bytes; 204 when search is unavailable or finds nothing.
func serveWebCover(c *gin.Context, abs, etag string, webSearch *imagesearch.Chain) {
	if webSearch == nil {
		c.Status(http.StatusNoContent)
		return
	}
	tags, _ := audiometa.ReadTags(abs) // best-effort; empty on parse failure
	data, ct, _, err := webSearch.Find(c.Request.Context(), audioCoverQuery(tags, abs))
	if err != nil || len(data) == 0 {
		c.Status(http.StatusNoContent)
		return
	}
	c.Header(CacheControl, CachePublicYear)
	c.Header("ETag", etag)
	c.Data(http.StatusOK, ct, data)
}

// audioCoverQuery builds the image-search query from tags, preferring
// "artist album" (the canonical album-art lookup), then "artist title", and
// finally the bare filename — so even untagged files get a reasonable guess.
func audioCoverQuery(t audiometa.Tags, abs string) string {
	if t.Artist != "" && t.Album != "" {
		return t.Artist + " " + t.Album + " album cover"
	}
	if t.Artist != "" && t.Title != "" {
		return t.Artist + " " + t.Title + " cover"
	}
	base := filepath.Base(abs)
	return strings.TrimSuffix(base, filepath.Ext(base)) + " album cover"
}
