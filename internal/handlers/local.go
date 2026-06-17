package handlers

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/localcache"
	"github.com/lgldsilva/jackui/internal/localstream"
	"github.com/lgldsilva/jackui/internal/renamer"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/tmdb"
	"github.com/lgldsilva/jackui/internal/transcode"
)

const (
	errMissingMountOrPathParam = "missing mount or path parameter"
	mountMeusDownloads         = "meus downloads"
	errOnlyMeusDownloads       = "Somente a área 'Meus downloads' pode ser modificada ou promovida"
)

// userFromCtx extracts the username from the JWT claims, returning ""
// only when auth is not enabled / the request is anonymous. Media tokens
// (scope="media") MUST keep their username: <video>/<img> load file, HLS,
// thumb and sidecar endpoints via ?token=, and those resolve paths on
// UserSubpath mounts — dropping the username here collapsed the per-user
// scope to the mount root, letting one user read another's files.
func userFromCtx(c *gin.Context) string {
	claims, ok := auth.ClaimsFromCtx(c)
	if !ok {
		return ""
	}
	return claims.Username
}

func checkMountAccess(b *local.Browser, c *gin.Context, mountName string) bool {
	username := userFromCtx(c)
	if !b.UserCanAccess(username, mountName) {
		c.JSON(http.StatusForbidden, gin.H{"error": "acesso negado a este mount"})
		return false
	}
	return true
}

// isAdminCtx reports whether the request is authenticated as an admin.
func isAdminCtx(c *gin.Context) bool {
	_, isAdmin, _ := auth.UserIDFromCtx(c)
	return isAdmin
}

// scopeUser returns the username whose per-user subdir the request operates on.
// Normally the caller themselves; an admin may target another user's space via
// ?user= (the "view as user" selector in the UI). The override is gated on the
// admin role — a non-admin passing ?user= is ignored, so it can never cross the
// per-user boundary. This is the single chokepoint for the admin cross-user
// access; everything that scopes a path must route through here.
func scopeUser(c *gin.Context) string {
	if target := c.Query("user"); target != "" && isAdminCtx(c) {
		return target
	}
	return userFromCtx(c)
}

// scopePath returns the path scoped to the user's subdir for UserSubpath mounts.
// For regular mounts, returns relPath unchanged.
func scopePath(b *local.Browser, c *gin.Context, mountName, relPath string) string {
	return b.UserScopedPath(mountName, relPath, scopeUser(c))
}

// LocalMounts handles GET /api/local/mounts -> []Mount
func LocalMounts(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		username := userFromCtx(c)
		// Enriquece cada mount com o espaço livre/total do filesystem (discos
		// físicos, rclone, etc) pra UI mostrar quanto dá pra usar.
		c.JSON(http.StatusOK, mountsWithSpace(b.MountsFor(username)))
	}
}

// LocalList handles GET /api/local/list?mount=NAME&path=REL -> []Entry.
// Entries the user has hidden are dropped unless the request opened the curtain
// (X-JackUI-Reveal-Hidden).
func LocalList(b *local.Browser, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount parameter"})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		username := scopeUser(c)
		scopedPath := b.UserScopedPath(mount, path, username)

		entries, err := b.List(mount, scopedPath)
		if err != nil {
			listHandleError(b, c, err)
			return
		}
		entries = b.StripUserScope(mount, username, entries)
		userID, _, _ := auth.UserIDFromCtx(c)
		entries = dropHiddenLocalEntries(entries, hiddenLocalSet(c, s, userID, mount))
		c.JSON(http.StatusOK, entries)
	}
}

func listHandleError(b *local.Browser, c *gin.Context, err error) {
	if isTraversalErr(err) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !os.IsNotExist(err) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// UserSubpath dir doesn't exist yet → return empty list (not 404)
	if b.IsUserSubpath(c.Query("mount")) {
		c.JSON(http.StatusOK, []local.Entry{})
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "path not found"})
}

// LocalFile handles GET /api/local/file?mount=NAME&path=REL/FILE
// Serves via http.ServeContent over a metered, read-ahead Session (Range
// requests preserved) so the UI can report transfer speed and rclone/Drive
// mounts get aligned reads. reg may be nil (falls back to a plain stream).
func LocalFile(b *local.Browser, reg *localstream.Registry, cache *localcache.Cache) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPathParam})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}

		scoped := scopePath(b, c, mount, path)
		abs, err := b.ResolvePath(mount, scoped)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// Prefer the locally-cached copy when ready: fast disk, no rclone EIO.
		abs = cachedAbs(cache, mount, scoped, abs)

		if !statLocalFile(c, abs) {
			return
		}

		setLocalFileSecurityHeaders(c, path)
		serveLocalFileMetered(c, reg, mount, scoped, abs)
	}
}

// serveLocalFileMetered opens abs and serves it through a metered Session so
// the direct-play path also reports speed. The session is registered under the
// direct transfer key for /local/transfer-status to find. Falls back to
// http.ServeFile when reg is nil (e.g. older tests).
func serveLocalFileMetered(c *gin.Context, reg *localstream.Registry, mount, scoped, abs string) {
	if reg == nil {
		http.ServeFile(c.Writer, c.Request, abs)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	key := transferKeyDirect(mount, scoped)
	sess := reg.OpenSolo(key, f, st.Size())
	defer reg.Release(key, sess)
	// ServeContent infers Content-Type from the file name (unless a header was
	// already forced by setLocalFileSecurityHeaders) and honours Range/HEAD.
	http.ServeContent(c.Writer, c.Request, filepath.Base(abs), st.ModTime(), sess)
}

// statLocalFile stats abs and writes the appropriate JSON error (returning
// false) when it's missing, unreadable, or a directory. Returns true when abs
// is a regular file ready to be served.
func statLocalFile(c *gin.Context, abs string) bool {
	stat, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": ErrFileNotFound})
			return false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	if stat.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrPathIsDir})
		return false
	}
	return true
}

// localMediaContentType maps media extensions to their correct MIME type. The
// container image has NO /etc/mime.types, so http.ServeContent can't deduce the
// type by extension and falls back to SNIFFING — and since we send
// X-Content-Type-Options: nosniff, the client (iOS Safari especially) trusts the
// header blindly. Sniffing gets .m4a wrong (detected as video/mp4), others empty,
// so iOS refuses to decode the audio and stalls at readyState 2 ("não toca").
// Setting the type explicitly here makes ServeContent keep it → direct-play works.
var localMediaContentType = map[string]string{
	".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac",
	".flac": "audio/flac", ".ogg": "audio/ogg", ".oga": "audio/ogg",
	".opus": "audio/opus", ".wav": "audio/wav", ".alac": "audio/mp4",
	".wma": "audio/x-ms-wma",
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm",
	".mov": "video/quicktime", ".ogv": "video/ogg",
}

// setLocalFileSecurityHeaders applies the stored-XSS guard for files served
// same-origin (where the JWT lives in localStorage). MIME sniffing is always
// disabled. Subtitles get the correct text/vtt so <track> renders them;
// actively rendered formats (html/svg/xml/js) are forced to download instead of
// executing in our origin. Media gets an explicit Content-Type (see
// localMediaContentType) since the image lacks /etc/mime.types.
func setLocalFileSecurityHeaders(c *gin.Context, path string) {
	c.Header("X-Content-Type-Options", "nosniff")
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".vtt", ".srt":
		c.Header(ContentType, "text/vtt; charset=utf-8")
	case ".html", ".htm", ".xhtml", ".svg", ".xml", ".js", ".mjs":
		c.Header(ContentType, "application/octet-stream")
		c.Header("Content-Disposition", "attachment; filename=\""+filepath.Base(path)+"\"")
	default:
		if ct := localMediaContentType[ext]; ct != "" {
			c.Header(ContentType, ct)
		}
	}
}

// localVideoExts mirrors the frontend's video detection — only these get a
// frame preview (ffmpeg on a non-video would just fail/waste work).
var localVideoExts = map[string]bool{
	".mp4": true, ".m4v": true, ".mkv": true, ".avi": true, ".mov": true, ".wmv": true,
	".webm": true, ".flv": true, ".mpeg": true, ".mpg": true, ".ts": true, ".m2ts": true,
}

func LocalThumb(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPathParam})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		if !localVideoExts[strings.ToLower(filepath.Ext(path))] {
			c.Status(http.StatusNoContent)
			return
		}
		abs, err := resolveLocalAbs(b, mount, scopePath(b, c, mount, path))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if abs == "" {
			c.Status(http.StatusNotFound)
			return
		}
		at := parseAt(c)
		cacheDir := localThumbCacheDir
		cachePath := thumbCachePath(cacheDir, abs, at)
		if serveCachedThumb(c, cachePath) {
			return
		}
		// A previous attempt failed (e.g. a 4K HDR HEVC frame ffmpeg can't decode
		// quickly): don't re-spend ~12s on every listing — serve 204 until the
		// marker expires. Cleared automatically once a thumb succeeds.
		if negativeThumbFresh(cachePath) {
			c.Status(http.StatusNoContent)
			return
		}
		out := captureThumb(c, abs, at, cacheDir, cachePath)
		if len(out) == 0 {
			c.Status(http.StatusNoContent)
			return
		}
		c.Header(CacheControl, CachePublicDay)
		c.Data(http.StatusOK, MIMEJPEG, out)
	}
}

func resolveLocalAbs(b *local.Browser, mount, path string) (string, error) {
	abs, err := b.ResolvePath(mount, path)
	if err != nil {
		return "", err
	}
	stat, err := os.Stat(abs)
	if err != nil || stat.IsDir() {
		return "", nil
	}
	return abs, nil
}

func parseAt(c *gin.Context) int {
	at := 10
	if v, e := strconv.Atoi(c.Query("at")); e == nil && v >= 0 {
		at = v
	}
	return at
}

// localThumbSem caps how many local-thumbnail ffmpeg jobs run at once. Listing
// a folder full of 4K HDR HEVC files would otherwise spawn one heavy ffmpeg per
// file simultaneously and peg the (often ARM) host — the symptom that froze the
// UI. 2 keeps things moving without serializing fully. Mirrors the health
// probe's semaphore pattern (internal/streamer/health.go).
var localThumbSem = make(chan struct{}, 2)

// localThumbCacheDir is where generated thumbnails AND negative-result markers
// live. Defaults to a temp dir (fine for tests); main() repoints it at the
// persistent stream DataDir via SetLocalThumbCacheDir so thumbs survive
// restarts instead of being regenerated every boot.
var localThumbCacheDir = filepath.Join(os.TempDir(), "jackui-local-thumbs")

const (
	localThumbTimeout = 12 * time.Second
	// negativeThumbTTL bounds how long a failed-capture marker suppresses
	// retries. Long enough to avoid hammering ffmpeg on every listing of a file
	// it can't decode; short enough that a transient failure self-heals.
	negativeThumbTTL = 24 * time.Hour
)

// SetLocalThumbCacheDir points the local-thumbnail cache at a persistent
// directory. Called once at startup; no-op on empty input.
func SetLocalThumbCacheDir(dir string) {
	if dir != "" {
		localThumbCacheDir = dir
	}
}

func thumbCachePath(cacheDir, abs string, at int) string {
	stat, _ := os.Stat(abs)
	key := fmt.Sprintf("%x", sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", abs, stat.ModTime().UnixNano(), at))))
	return filepath.Join(cacheDir, key+".jpg")
}

// negativeMarkerPath is the sibling marker recording a failed capture for a
// given cache key (so we don't retry a doomed ffmpeg on every listing).
func negativeMarkerPath(cachePath string) string { return cachePath + ".empty" }

// negativeThumbFresh reports whether a recent failed-capture marker exists,
// meaning we should short-circuit to 204 instead of re-running ffmpeg.
func negativeThumbFresh(cachePath string) bool {
	st, err := os.Stat(negativeMarkerPath(cachePath))
	if err != nil {
		return false
	}
	return time.Since(st.ModTime()) < negativeThumbTTL
}

func serveCachedThumb(c *gin.Context, cachePath string) bool {
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return false
	}
	c.Header(CacheControl, CachePublicDay)
	c.Data(http.StatusOK, MIMEJPEG, data)
	return true
}

func captureThumb(c *gin.Context, abs string, at int, cacheDir, cachePath string) []byte {
	ctx, cancel := context.WithTimeout(c.Request.Context(), localThumbTimeout)
	defer cancel()
	// Limit concurrent ffmpeg jobs; if the client navigates away before a slot
	// frees up, bail cheaply rather than piling on more heavy 4K decodes.
	select {
	case localThumbSem <- struct{}{}:
		defer func() { <-localThumbSem }()
	case <-ctx.Done():
		return nil
	}
	seeks := []int{at}
	if at != 1 {
		seeks = append(seeks, 1)
	}
	var out []byte
	for _, s := range seeks {
		cmd := exec.CommandContext(ctx, ffBinary,
			ffHideBanner, ffLogLevel, "error",
			"-ss", strconv.Itoa(s),
			"-i", abs,
			"-frames:v", "1",
			"-vf", "scale=320:-2",
			"-q:v", "5",
			"-f", "mjpeg",
			"-y", pipe1,
		)
		if data, cerr := cmd.Output(); cerr == nil && len(data) > 0 {
			out = data
			break
		}
	}
	if os.MkdirAll(cacheDir, 0o755) != nil {
		return out
	}
	if len(out) > 0 {
		_ = os.WriteFile(cachePath, out, 0o644)
		_ = os.Remove(negativeMarkerPath(cachePath)) // a success clears any stale failure
		return out
	}
	// Persist the FAILURE so repeated listings don't re-run a ~12s decode for a
	// frame ffmpeg can't produce — but only on a real ffmpeg error/timeout, not
	// when the client cancelled (navigated away) before we finished.
	if ctx.Err() != context.Canceled {
		_ = os.WriteFile(negativeMarkerPath(cachePath), nil, 0o644)
	}
	return out
}

// LocalTranscode handles GET /api/local/transcode?mount=NAME&path=REL
// Transcodes a local file to H.264/AAC fragmented MP4 so browsers can play
// formats like MKV, AVI or any container not natively supported.
func LocalTranscode(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPathParam})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		abs, err := b.ResolvePath(mount, scopePath(b, c, mount, path))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		stat, err := os.Stat(abs)
		if err != nil || stat.IsDir() {
			c.JSON(http.StatusNotFound, gin.H{"error": ErrFileNotFound})
			return
		}
		f, err := os.Open(abs)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer func() { _ = f.Close() }()

		opts := transcode.Options{
			AudioTrack:   -1,
			SubBurnTrack: -1,
			VideoCodec:   "h264",
			AudioCodec:   "aac",
			Container:    "mp4",
		}
		if err := transcode.Run(c.Request.Context(), f, c.Writer, opts); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
	}
}

func isTraversalErr(err error) bool {
	s := err.Error()
	return strings.Contains(s, "traversal") ||
		strings.Contains(s, "must be relative") ||
		strings.Contains(s, "mount") && strings.Contains(s, "not found")
}

func LocalDelete(b *local.Browser, dls *downloads.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPathParam})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		if !canModifyMount(c, mount) {
			return
		}
		abs, err := resolveDeletablePath(b, mount, scopePath(b, c, mount, path))
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "file or directory not found"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// Find the torrent(s) linked to this path BEFORE deleting it (the lookup
		// matches on the on-disk file_path). Deleting a local file/folder must
		// also tear down its torrent so it doesn't linger in Downloads, in the
		// piece cache or as a favorite.
		linked, _ := dls.FindByPathPrefix(abs)
		if err := os.RemoveAll(abs); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to delete: %s", err.Error())})
			return
		}
		removed := purgeLinkedTorrents(dls, s, linked)
		c.JSON(http.StatusOK, gin.H{"message": "deleted successfully", "torrentsRemoved": removed})
	}
}

// LocalCleanEmptyDirs handles POST /api/local/clean-empty — removes empty
// subdirectories under the given path (or the mount root when path is empty).
// Same access model as delete: writable mount ("meus downloads") or admin only.
// Only removes truly-empty dirs, never the starting dir or the mount root.
func LocalCleanEmptyDirs(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		if mount == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPathParam})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		if !canModifyMount(c, mount) {
			return
		}
		cleaned, err := b.RemoveEmptyDirs(mount, scopePath(b, c, mount, c.Query("path")))
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"cleaned": cleaned})
	}
}

type folderLockReq struct {
	Mount  string `json:"mount"`
	Path   string `json:"path"`
	Locked bool   `json:"locked"`
}

// LocalSetFolderLock handles POST /api/local/lock — pins/unpins a folder so the
// "clean empty folders" sweep keeps it even with no files inside (a ".keep"
// marker). Same access model as delete/clean: a writable mount ("meus
// downloads") or admin. The mount root can't be pinned.
func LocalSetFolderLock(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req folderLockReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.Mount == "" || req.Path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPathParam})
			return
		}
		if !checkMountAccess(b, c, req.Mount) {
			return
		}
		if !canModifyMount(c, req.Mount) {
			return
		}
		if err := b.SetFolderLock(req.Mount, scopePath(b, c, req.Mount, req.Path), req.Locked); err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"locked": req.Locked})
	}
}

// purgeLinkedTorrents tears down the torrent(s) tied to a deleted local path:
// drops the active torrent, wipes its piece cache, clears the favorite, and
// removes the download row. Returns how many rows were removed. Best-effort —
// a failure on one step doesn't abort the others.
func purgeLinkedTorrents(dls *downloads.Store, s *streamer.Streamer, linked []downloads.Download) int {
	removed := 0
	for _, d := range linked {
		dropTorrentFromStreamer(s, d)
		if err := dls.Delete(d.UserID, d.ID); err == nil {
			removed++
		}
	}
	return removed
}

// dropTorrentFromStreamer tears down a single torrent's streamer-side state:
// drops the active torrent, wipes its piece cache, and clears the favorite.
// Best-effort — each step is independent.
func dropTorrentFromStreamer(s *streamer.Streamer, d downloads.Download) {
	if s == nil {
		return
	}
	if d.InfoHash != "" {
		var h metainfo.Hash
		if err := h.FromHexString(d.InfoHash); err == nil {
			s.Drop(h)
		}
	}
	if d.Name == "" {
		return
	}
	_ = s.ClearEntry(d.Name) // wipe pieces from the cache (DataDir/<name>)
	if favs := s.Favorites(); favs != nil {
		_ = favs.Remove(d.Name, d.UserID, true)
	}
}

// relinkMovedTorrents keeps the torrent↔file link intact after a local file or
// folder is moved/renamed (promote, reclassify or move-between-mounts). For every
// download whose on-disk path sat under oldAbs it rewrites file_path to the new
// location — so the FilePathResolver (and thus the next play) resolves the file
// where it now lives — and drops the active torrent so the streamer re-resolves +
// re-verifies pieces at the new path on the next access (~free when the file is
// already complete), instead of holding a stale descriptor on the old path. The
// piece cache and the favorite (both keyed by torrent name, not file_path) are
// left untouched. Best-effort: a failure on one row doesn't abort the rest.
func relinkMovedTorrents(dls *downloads.Store, s *streamer.Streamer, oldAbs, newAbs string) int {
	if dls == nil {
		return 0
	}
	linked, _ := dls.FindByPathPrefix(oldAbs)
	relinked := 0
	for _, d := range linked {
		newPath := newAbs + strings.TrimPrefix(d.FilePath, oldAbs)
		if err := dls.SetFilePath(d.UserID, d.ID, newPath); err != nil {
			continue
		}
		if s != nil && d.InfoHash != "" {
			var h metainfo.Hash
			if err := h.FromHexString(d.InfoHash); err == nil {
				s.Drop(h)
			}
		}
		relinked++
	}
	return relinked
}

func canModifyMount(c *gin.Context, mount string) bool {
	claims, _ := auth.ClaimsFromCtx(c)
	isAdmin := claims != nil && claims.Role == auth.RoleAdmin
	if isAdmin || strings.ToLower(mount) == mountMeusDownloads {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{"error": errOnlyMeusDownloads})
	return false
}

func resolveDeletablePath(b *local.Browser, mount, path string) (string, error) {
	cleanPath := filepath.Clean(path)
	if cleanPath == "" || cleanPath == "." || cleanPath == "/" {
		return "", fmt.Errorf("cannot delete mount root")
	}
	abs, err := b.ResolvePath(mount, path)
	if err != nil {
		return "", err
	}
	if isMountRoot(b, abs) {
		return "", fmt.Errorf("cannot delete mount root")
	}
	if _, err := os.Stat(abs); err != nil {
		return "", err
	}
	return abs, nil
}

func isMountRoot(b *local.Browser, abs string) bool {
	for _, m := range b.Mounts() {
		mountAbs, err := filepath.Abs(m.Path)
		if err == nil && abs == mountAbs {
			return true
		}
	}
	return false
}

func LocalPromote(b *local.Browser, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []PromoteDest, dls *downloads.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": errSharedDirNotConfig})
			return
		}
		req, base, ok := extractLocalPromoteReq(c, b, sharedDir, dests)
		if !ok {
			return
		}
		targetDir, err := localPromoteTargetDir(base, req.TargetSubdir)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		username := scopeUser(c)
		orig := originalLocalPaths(req)
		paths := resolveLocalPaths(b, req, username)
		if len(paths) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "nenhum arquivo para promover"})
			return
		}
		deps := &promoteDstDeps{
			ctx: c.Request.Context(), aiClient: aiClient, tmdbClient: tmdbClient,
			base: base, mount: req.Mount, dls: dls, s: s,
			overrides: scopedOverrides(b, req, username),
		}
		moved, errs, results := execPromoteMoves(b, deps, req.Mount, paths, orig, targetDir)
		// `errors` keeps the legacy {path,error} list (single-item callers);
		// `results` is the per-item batch feedback (success/error keyed by the
		// ORIGINAL un-scoped path the UI sent, so the reclassify table can mark
		// each row). path in errors is also the original path now.
		status := http.StatusOK
		if moved == 0 {
			status = http.StatusUnprocessableEntity
		}
		c.JSON(status, gin.H{"moved": moved, "failed": len(errs), "errors": errs, "results": results})
	}
}

// originalLocalPaths returns the un-scoped source paths exactly as the UI sent
// them (Paths first, else the single Path), so per-item results can be reported
// against the same keys the client knows — not the user-scoped variants.
func originalLocalPaths(req *localPromoteReq) []string {
	if len(req.Paths) > 0 {
		return req.Paths
	}
	if req.Path != "" {
		return []string{req.Path}
	}
	return nil
}

func localPromoteTargetDir(base, subdirStr string) (string, error) {
	subdir, err := sanitizeSubdir(subdirStr)
	if err != nil {
		return "", err
	}
	if subdir == "" {
		return base, nil
	}
	return filepath.Join(base, subdir), nil
}

// execPromoteMoves moves each scoped path, returning the success count, the
// legacy {path,error} failure list, and a per-item results list (one entry per
// input, ok=true/false). Both the error list and the results report the
// ORIGINAL un-scoped path (orig[i]) so the caller can key feedback by what the
// UI sent. orig may be shorter than paths (older callers pass nil) — it then
// falls back to the scoped path.
func execPromoteMoves(b *local.Browser, deps *promoteDstDeps, mount string, paths, orig []string, targetDir string) (int, []gin.H, []gin.H) {
	moved := 0
	errs := make([]gin.H, 0)
	results := make([]gin.H, 0, len(paths))
	for i, scopedRel := range paths {
		key := scopedRel
		if i < len(orig) && orig[i] != "" {
			key = orig[i]
		}
		if e := promoteOnePath(b, deps, mount, scopedRel, targetDir); e != nil {
			e["path"] = key
			errs = append(errs, e)
			results = append(results, gin.H{"path": key, "ok": false, "error": e["error"]})
		} else {
			moved++
			results = append(results, gin.H{"path": key, "ok": true})
		}
	}
	return moved, errs, results
}

// promoteOnePath moves one already-scoped relative path into targetDir, applying
// the AI rename via computePromoteDst. Returns nil on success (incl. a no-op when
// already in place) or a {path,error} map describing the failure.
func promoteOnePath(b *local.Browser, deps *promoteDstDeps, mount, scopedRel, targetDir string) gin.H {
	clean := filepath.Clean(scopedRel)
	if clean == "" || clean == "." || clean == "/" {
		return gin.H{"path": scopedRel, "error": "cannot promote mount root"}
	}
	src, err := b.ResolvePath(mount, scopedRel)
	if err != nil {
		return gin.H{"path": scopedRel, "error": err.Error()}
	}
	stat, err := os.Stat(src)
	if err != nil {
		return gin.H{"path": scopedRel, "error": "arquivo de origem não existe"}
	}
	baseName := filepath.Base(src)
	dst, dir := computePromoteDst(deps, baseName, scopedRel, targetDir)
	if src == dst {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return gin.H{"path": scopedRel, "error": "criar destino: " + err.Error()}
	}
	if err := movePath(src, dst, stat); err != nil {
		// Remove the empty dir we created if the move failed — avoids orphan dirs
		// (e.g. FUSE mounts that reject cross-device writes).
		_ = os.Remove(filepath.Dir(dst))
		return gin.H{"path": scopedRel, "error": "mover arquivo: " + err.Error()}
	}
	relinkMovedTorrents(deps.dls, deps.s, src, dst)
	return nil
}

// computePromoteDst decides where a file lands. Precedence:
//  1. a valid user override (the edited target from the reclassify table) —
//     sanitized + de-conflicted, never escaping base;
//  2. the AI/TMDB suggestion (location-aware via LocalContext);
//  3. the plain targetDir/baseName fallback.
// scopedRel is the SCOPED source path — used to look up the override and to give
// the AI its location hint (currentDirOf).
func computePromoteDst(d *promoteDstDeps, baseName, scopedRel, targetDir string) (string, string) {
	lc := localContextFor(d.base, d.mount, currentDirOf(scopedRel))
	if rel, ok := overrideTargetRel(d, scopedRel, lc); ok {
		targetRel := renamer.ResolveTargetConflict(d.base, rel)
		dst := filepath.Join(d.base, targetRel)
		return dst, filepath.Dir(dst)
	}
	if d.aiClient != nil {
		preview, err := renamer.GeneratePreviewWithContext(d.ctx, d.aiClient, d.tmdbClient, baseName, lc)
		if err == nil && preview != nil {
			targetRel := renamer.ResolveTargetConflict(d.base, preview.TargetPath)
			dst := filepath.Join(d.base, targetRel)
			return dst, filepath.Dir(dst)
		}
	}
	return filepath.Join(targetDir, baseName), targetDir
}

// overrideTargetRel returns the sanitized override target (relative to base) for
// the scoped source path, when the request carried a non-empty, valid one. The
// guard is sanitizeOverrideTarget — the SAME path-traversal protection the AI
// path goes through (per-segment sanitizeFilename + reject "..", absolute or
// base escape) plus case-insensitive category-folder reuse.
func overrideTargetRel(d *promoteDstDeps, scopedRel string, lc *renamer.LocalContext) (string, bool) {
	if len(d.overrides) == 0 {
		return "", false
	}
	raw, ok := d.overrides[scopedRel]
	if !ok || strings.TrimSpace(raw) == "" {
		return "", false
	}
	return sanitizeOverrideTarget(raw, lc)
}

// currentDirOf returns the directory portion of a relative path for the AI
// location hint ("" when the item is at the mount root). Defensive against the
// filepath.Dir "." sentinel.
func currentDirOf(rel string) string {
	dir := filepath.Dir(filepath.Clean(rel))
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

// localContextFor builds the renamer.LocalContext from the destination base and
// the source location: a shallow ReadDir of the destination (top-level folders
// only) so the renamer can reuse an existing category, plus the current path
// for the AI's location hint. Cheap: a single ReadDir, results truncated. A
// missing/unreadable base degrades to nil (legacy hardcoded labels).
func localContextFor(base, mount, currentPath string) *renamer.LocalContext {
	entries, err := os.ReadDir(base)
	if err != nil {
		if currentPath == "" {
			return nil
		}
		return &renamer.LocalContext{CurrentPath: currentPath, MountName: mount}
	}
	folders := listDirs(entries)
	if len(folders) > maxPromoteContextFolders {
		folders = folders[:maxPromoteContextFolders]
	}
	return &renamer.LocalContext{
		CurrentPath: currentPath,
		MountName:   mount,
		DestFolders: folders,
	}
}

// maxPromoteContextFolders caps the top-level folder listing handed to the
// renamer/AI so a huge library never blows up the prompt or the work.
const maxPromoteContextFolders = 40

func LocalPromotePreview(b *local.Browser, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": errSharedDirNotConfig})
			return
		}
		req, base, ok := extractLocalPromoteReq(c, b, sharedDir, dests)
		if !ok {
			return
		}
		orig := originalLocalPaths(req)
		paths := resolveLocalPaths(b, req, scopeUser(c))
		previews := buildLocalPreviews(&localPreviewDeps{c: c, b: b, aiClient: aiClient, tmdbClient: tmdbClient, mount: req.Mount, base: base}, paths, orig)
		c.JSON(http.StatusOK, gin.H{"previews": previews})
	}
}

type localPreviewDeps struct {
	c          *gin.Context
	b          *local.Browser
	aiClient   *ai.Client
	tmdbClient *tmdb.Client
	mount      string
	base       string
}

type promoteDstDeps struct {
	ctx        context.Context
	aiClient   *ai.Client
	tmdbClient *tmdb.Client
	base       string
	mount      string             // source mount name, for the AI location hint
	dls        *downloads.Store   // to re-link a moved file's torrent (may be nil)
	s          *streamer.Streamer // to drop the active torrent so it re-verifies
	// overrides maps a SCOPED source path to the user-edited destination path
	// (relative to base). When set for the item being moved, the edited target
	// REPLACES the AI suggestion — after the same sanitize/anti-traversal guard.
	overrides map[string]string
}

type localPromoteReq struct {
	Mount        string   `json:"mount"`
	Path         string   `json:"path"`
	Paths        []string `json:"paths"`
	TargetSubdir string   `json:"targetSubdir"`
	TargetBase   string   `json:"targetBase"`
	RenameIA     bool     `json:"renameIA"`
	// Overrides maps a source path (the un-scoped relative path the UI sent in
	// Paths/Path) to the user-edited destination path, RELATIVE to the resolved
	// base. When present for an item, the edited target REPLACES the AI's
	// suggestion — but it is sanitized exactly like the AI path (sanitizeOverrideTarget:
	// per-segment sanitizeFilename, reject "..", absolute or base-escaping, reuse
	// an existing category folder case-insensitively) before any move. An empty
	// or invalid override silently falls back to the AI computation.
	Overrides map[string]string `json:"overrides"`
}

func extractLocalPromoteReq(c *gin.Context, b *local.Browser, sharedDir string, dests []PromoteDest) (*localPromoteReq, string, bool) {
	var req localPromoteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidData})
		return nil, "", false
	}
	if req.Mount == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount parameter"})
		return nil, "", false
	}
	if !checkMountAccess(b, c, req.Mount) {
		return nil, "", false
	}
	if !canModifyMount(c, req.Mount) {
		return nil, "", false
	}
	base, err := resolveTargetBase(req.TargetBase, sharedDir, dests)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, "", false
	}
	return &req, base, true
}

func resolveLocalPaths(b *local.Browser, req *localPromoteReq, username string) []string {
	scope := func(p string) string { return b.UserScopedPath(req.Mount, p, username) }
	if len(req.Paths) > 0 {
		out := make([]string, len(req.Paths))
		for i, p := range req.Paths {
			out[i] = scope(p)
		}
		return out
	}
	if req.Path != "" {
		return []string{scope(req.Path)}
	}
	return nil
}

// buildLocalPreviews builds one preview per scoped path. orig (when present)
// carries the matching un-scoped path the UI sent; the preview's reported
// `path` uses it so the reclassify table can key rows and round-trip the same
// value back as an override. orig may be nil/shorter — it falls back to the
// scoped path.
func buildLocalPreviews(d *localPreviewDeps, paths, orig []string) []gin.H {
	if len(paths) == 0 {
		return []gin.H{}
	}
	previews := make([]gin.H, 0, len(paths))
	for i, p := range paths {
		key := p
		if i < len(orig) && orig[i] != "" {
			key = orig[i]
		}
		previews = append(previews, previewItem(d, p, key))
	}
	return previews
}

func previewItem(d *localPreviewDeps, p, key string) gin.H {
	cleanPath := filepath.Clean(p)
	if cleanPath == "" || cleanPath == "." || cleanPath == "/" {
		return gin.H{"path": key, "error": "cannot promote mount root"}
	}

	src, err := d.b.ResolvePath(d.mount, p)
	if err != nil {
		return gin.H{"path": key, "error": err.Error()}
	}

	if _, err := os.Stat(src); err != nil {
		return gin.H{"path": key, "error": "arquivo não existe"}
	}

	baseName := filepath.Base(src)
	lc := localContextFor(d.base, d.mount, currentDirOf(p))
	preview, err := renamer.GeneratePreviewWithContext(d.c.Request.Context(), d.aiClient, d.tmdbClient, baseName, lc)
	if err != nil {
		return gin.H{"path": key, "error": err.Error()}
	}

	nonConflicting := renamer.ResolveTargetConflict(d.base, preview.TargetPath)
	return gin.H{
		"path":         key,
		"originalName": baseName,
		"cleanName":    preview.CleanName,
		"targetPath":   nonConflicting,
		"kind":         preview.Kind,
		"year":         preview.Year,
		"season":       preview.Season,
		"episode":      preview.Episode,
		"episodeName":  preview.EpisodeName,
		"reusedFolder": preview.ReusedFolder,
	}
}

// LocalWalk handles GET /api/local/walk?mount=&path=&media_only=
// Recursively lists all files under a directory in a mount.
func LocalWalk(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPath})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		mediaOnly := c.Query("media_only") == "true" || c.Query("media_only") == "1"
		entries, err := b.Walk(mount, scopePath(b, c, mount, path), mediaOnly)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "path not found"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if entries == nil {
			entries = []local.Entry{}
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries, "total": len(entries)})
	}
}

// LocalUpload handles POST /api/local/upload?mount=NAME&path=REL
// allowedUploadExts restringe uploads locais a tipos de mídia + legenda. O
// serving (LocalFile) já força download de não-mídia p/ barrar XSS armazenado;
// isto é defesa em profundidade na entrada (rejeita .html/.js/.svg etc.).
var allowedUploadExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true,
	".webm": true, ".ts": true, ".m2ts": true, ".mpg": true, ".mpeg": true,
	".wmv": true, ".flv": true, ".ogv": true, ".3gp": true,
	".srt": true, ".vtt": true, ".ass": true, ".ssa": true, ".sub": true,
}

func LocalUpload(b *local.Browser, maxUploadBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")

		if mount == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "mount is required"})
			return
		}

		if !canModifyMount(c, mount) {
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}

		// Teto de tamanho (anti disk-fill): MaxBytesReader corta a leitura do
		// corpo inteiro (multipart incluso) antes de escrever no disco.
		if maxUploadBytes > 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)
		}

		fileHeader, filename, ok := validateUpload(c, maxUploadBytes)
		if !ok {
			return
		}

		absDir, absPath, ok := resolveUploadDest(c, b, mount, path, filename)
		if !ok {
			return
		}

		finalName, ok := streamUploadToDisk(c, fileHeader, absDir, absPath, filename)
		if !ok {
			return
		}
		c.JSON(http.StatusCreated, gin.H{"uploaded": finalName, "path": filepath.Join(path, finalName)})
	}
}

// streamUploadToDisk abre o arquivo enviado, garante o diretório de destino e
// grava em disco com claim atômico (createUploadFile faz o auto-rename em
// colisão). Em erro responde o JSON apropriado e retorna ok=false.
func streamUploadToDisk(c *gin.Context, fileHeader *multipart.FileHeader, absDir, absPath, filename string) (string, bool) {
	srcFile, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "erro ao abrir arquivo enviado: " + err.Error()})
		return "", false
	}
	defer srcFile.Close()

	if err := os.MkdirAll(absDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "erro ao criar diretório: " + err.Error()})
		return "", false
	}

	dstFile, finalPath, ok := createUploadFile(c, absDir, absPath, filename)
	if !ok {
		return "", false
	}
	defer dstFile.Close()

	if _, err = io.Copy(dstFile, srcFile); err != nil {
		_ = os.Remove(finalPath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "erro ao gravar arquivo: " + err.Error()})
		return "", false
	}
	return filepath.Base(finalPath), true
}

// validateUpload pulls the "file" part, validates its name and extension, and
// enforces the size ceiling. It writes the JSON error and returns ok=false on
// any failure; on success returns the header and the sanitized base filename.
func validateUpload(c *gin.Context, maxUploadBytes int64) (fileHeader *multipart.FileHeader, filename string, ok bool) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required: " + err.Error()})
		return nil, "", false
	}

	filename = filepath.Base(fileHeader.Filename)
	if filename == "" || filename == "." || filename == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
		return nil, "", false
	}

	if !allowedUploadExts[strings.ToLower(filepath.Ext(filename))] {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "tipo de arquivo não permitido (apenas vídeo/legenda)"})
		return nil, "", false
	}

	// Rejeição amigável e barata antes de ler o corpo (o MaxBytesReader
	// acima é a garantia dura; isto evita gravar parcial p/ um Size já grande).
	if maxUploadBytes > 0 && fileHeader.Size > maxUploadBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": fmt.Sprintf("arquivo excede o limite de %d MB", maxUploadBytes/(1<<20))})
		return nil, "", false
	}

	return fileHeader, filename, true
}

// resolveUploadDest resolves the user-scoped destination directory and the
// target file path, guarding against path traversal. Writes the JSON error and
// returns ok=false on failure.
func resolveUploadDest(c *gin.Context, b *local.Browser, mount, path, filename string) (absDir, absPath string, ok bool) {
	scoped := b.UserScopedPath(mount, path, scopeUser(c))
	absDir, err := b.ResolvePath(mount, scoped)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "caminho de destino inválido: " + err.Error()})
		return "", "", false
	}

	absPath = filepath.Join(absDir, filename)
	if !strings.HasPrefix(absPath, absDir) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path traversal detectado"})
		return "", "", false
	}

	return absDir, absPath, true
}

// createUploadFile creates the destination file, auto-renaming on collision so
// one user never clobbers another's file (the destination dir may be shared).
// O_EXCL makes claim+create atomic, so two concurrent uploads of the same name
// resolve to distinct files ("foo.mkv" → "foo (1).mkv" → ...) instead of one
// overwriting the other. Writes the JSON error and returns ok=false on failure.
func createUploadFile(c *gin.Context, absDir, absPath, filename string) (dstFile *os.File, finalPath string, ok bool) {
	ext := filepath.Ext(filename)
	stem := strings.TrimSuffix(filename, ext)
	finalPath = absPath
	for i := 1; ; i++ {
		f, err := os.OpenFile(finalPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
		if err == nil {
			return f, finalPath, true
		}
		if !os.IsExist(err) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "erro ao criar arquivo no servidor: " + err.Error()})
			return nil, "", false
		}
		if i > 9999 {
			c.JSON(http.StatusConflict, gin.H{"error": "muitos arquivos com o mesmo nome neste diretório"})
			return nil, "", false
		}
		finalPath = filepath.Join(absDir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
	}
}

type moveEntryReq struct {
	SrcMount string `json:"srcMount"`
	SrcPath  string `json:"srcPath"`
	DstMount string `json:"dstMount"`
	DstPath  string `json:"dstPath"`
}

// LocalMoveEntry handles POST /api/local/move — moves a file or directory
// from one mount to another (or within the same mount). Admin only.
// Body: { srcMount, srcPath, dstMount, dstPath (target directory) }
func LocalMoveEntry(b *local.Browser, dls *downloads.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		localMoveHandler(c, b, dls, s)
	}
}

func localMoveHandler(c *gin.Context, b *local.Browser, dls *downloads.Store, s *streamer.Streamer) {
	var req moveEntryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.SrcMount == "" || req.SrcPath == "" || req.DstMount == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "srcMount, srcPath and dstMount are required"})
		return
	}

	if !isAdminMove(c) {
		return
	}
	if !checkMountAccess(b, c, req.SrcMount) || !checkMountAccess(b, c, req.DstMount) {
		return
	}

	srcAbs, srcStat, err := resolveSource(b, c, &req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dstAbs, err := resolveDest(b, c, &req, srcAbs)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if isSelfMove(srcStat, srcAbs, dstAbs) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "não é possível mover uma pasta para dentro de si mesma"})
		return
	}

	// Refuse to clobber: os.Rename (and the cross-device copy fallback) would
	// silently overwrite/merge an existing item of the same name at the
	// destination — data loss while the UI reports success. Make the caller
	// rename or pick another folder.
	if _, err := os.Stat(dstAbs); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "já existe um item com esse nome no destino"})
		return
	}

	if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "criar diretório destino: " + err.Error()})
		return
	}
	if err := movePath(srcAbs, dstAbs, srcStat); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "mover: " + err.Error()})
		return
	}
	relinked := relinkMovedTorrents(dls, s, srcAbs, dstAbs)
	c.JSON(http.StatusOK, gin.H{"moved": filepath.Join(req.DstMount, req.DstPath, filepath.Base(req.SrcPath)), "relinked": relinked})
}

func isAdminMove(c *gin.Context) bool {
	claims, _ := auth.ClaimsFromCtx(c)
	if claims == nil || claims.Role != auth.RoleAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "apenas admins podem mover entre mounts"})
		return false
	}
	return true
}

func resolveSource(b *local.Browser, c *gin.Context, req *moveEntryReq) (string, os.FileInfo, error) {
	// Apply user subpath scoping for mounts like "Meus downloads" where each
	// user sees/writes only their own subdir. The frontend strips the prefix
	// (via StripUserScope in LocalList) so we must re-add it here.
	scopedSrc := b.UserScopedPath(req.SrcMount, req.SrcPath, scopeUser(c))
	srcAbs, err := b.ResolvePath(req.SrcMount, scopedSrc)
	if err != nil {
		return "", nil, fmt.Errorf("origem: %w", err)
	}
	srcStat, err := os.Stat(srcAbs)
	if err != nil {
		return "", nil, fmt.Errorf("origem não encontrada")
	}
	return srcAbs, srcStat, nil
}

func resolveDest(b *local.Browser, c *gin.Context, req *moveEntryReq, srcAbs string) (string, error) {
	dstDirRel := req.DstPath
	if dstDirRel == "" {
		dstDirRel = "."
	}
	// Apply user subpath scoping for UserSubpath destination mounts.
	scopedDst := b.UserScopedPath(req.DstMount, dstDirRel, scopeUser(c))
	dstDirAbs, err := b.ResolvePath(req.DstMount, scopedDst)
	if err != nil {
		return "", fmt.Errorf("destino: %w", err)
	}
	return filepath.Join(dstDirAbs, filepath.Base(srcAbs)), nil
}

func isSelfMove(srcStat os.FileInfo, srcAbs, dstAbs string) bool {
	return srcStat.IsDir() && strings.HasPrefix(dstAbs+string(filepath.Separator), srcAbs+string(filepath.Separator))
}

type renameEntryReq struct {
	Mount   string `json:"mount"`
	Path    string `json:"path"`
	NewName string `json:"newName"`
}

// LocalRename handles POST /api/local/rename — renames a file or folder in
// place (same parent directory). Same access model as delete: a writable mount
// ("meus downloads") or admin. NewName must be a bare file name (no separators,
// no traversal) and the mount root can't be renamed. Reuses movePath +
// relinkMovedTorrents so a renamed download keeps its torrent link and replays.
func LocalRename(b *local.Browser, dls *downloads.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := bindRenameReq(c)
		if !ok {
			return
		}
		if !checkMountAccess(b, c, req.Mount) || !canModifyMount(c, req.Mount) {
			return
		}
		srcAbs, stat, ok := resolveRenameSource(b, c, req)
		if !ok {
			return
		}
		dstAbs, ok := resolveRenameDest(c, srcAbs, req.NewName)
		if !ok {
			return
		}
		if err := movePath(srcAbs, dstAbs, stat); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "renomear: " + err.Error()})
			return
		}
		relinked := relinkMovedTorrents(dls, s, srcAbs, dstAbs)
		c.JSON(http.StatusOK, gin.H{"renamed": filepath.Base(dstAbs), "relinked": relinked})
	}
}

// bindRenameReq parses + validates the rename request (required fields and a
// safe bare name). Writes the 400 and returns ok=false on any problem.
func bindRenameReq(c *gin.Context) (renameEntryReq, bool) {
	var req renameEntryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return req, false
	}
	if req.Mount == "" || req.Path == "" || req.NewName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mount, path and newName are required"})
		return req, false
	}
	if !isValidRenameName(req.NewName) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nome inválido: não pode conter barras nem '..'"})
		return req, false
	}
	return req, true
}

// resolveRenameSource resolves the source path (scoped + traversal-guarded) and
// stats it. Writes the error response and returns ok=false on failure.
func resolveRenameSource(b *local.Browser, c *gin.Context, req renameEntryReq) (string, os.FileInfo, bool) {
	srcAbs, err := resolveDeletablePath(b, req.Mount, scopePath(b, c, req.Mount, req.Path))
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "file or directory not found"})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		}
		return "", nil, false
	}
	stat, err := os.Stat(srcAbs)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file or directory not found"})
		return "", nil, false
	}
	return srcAbs, stat, true
}

// resolveRenameDest builds the destination path (same parent dir, new bare
// name) and refuses a no-op or a clobber. ok=false on failure (response sent).
func resolveRenameDest(c *gin.Context, srcAbs, newName string) (string, bool) {
	dstAbs := filepath.Join(filepath.Dir(srcAbs), newName)
	if dstAbs == srcAbs {
		c.JSON(http.StatusBadRequest, gin.H{"error": "o novo nome é igual ao atual"})
		return "", false
	}
	if _, err := os.Stat(dstAbs); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "já existe um item com esse nome"})
		return "", false
	}
	return dstAbs, true
}

// isValidRenameName reports whether name is a safe bare file name: not empty,
// not a traversal token, and free of path separators. filepath.Base collapses
// any path to its last element, so equality proves there was no separator to
// begin with (covers both / and the OS separator).
func isValidRenameName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	return filepath.Base(name) == name
}

// movePath handles moving files and directories, even across different filesystems/mounts.
func movePath(src, dst string, stat os.FileInfo) error {
	// First try renaming. It works if on the same volume/filesystem.
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}

	// If rename fails (e.g. cross-device link), copy and delete
	if stat.IsDir() {
		return copyDirAndRemove(src, dst, stat)
	}
	return copyFileAndRemove(src, dst, stat)
}

func copyFileAndRemove(src, dst string, stat os.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, stat.Mode())
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err = io.Copy(out, in); err != nil {
		_ = os.Remove(dst)
		return err
	}

	_ = out.Close()
	_ = in.Close()
	// Preserve the original mtime — os.Rename keeps it, but this cross-device
	// fallback (→ rclone/GDrive, other disk) would otherwise stamp "now",
	// breaking date sort and mtime-based scans.
	_ = os.Chtimes(dst, stat.ModTime(), stat.ModTime())
	return os.Remove(src)
}

func copyDirAndRemove(src, dst string, stat os.FileInfo) error {
	if err := os.MkdirAll(dst, stat.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		info, err := entry.Info()
		if err != nil {
			return err
		}

		if entry.IsDir() {
			if err := copyDirAndRemove(srcPath, dstPath, info); err != nil {
				return err
			}
		} else {
			if err := copyFileAndRemove(srcPath, dstPath, info); err != nil {
				return err
			}
		}
	}

	// Preserve the directory's mtime too (see copyFileAndRemove).
	_ = os.Chtimes(dst, stat.ModTime(), stat.ModTime())
	// After recursive copying, remove the source directory completely
	return os.RemoveAll(src)
}
