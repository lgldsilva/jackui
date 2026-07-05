package local

import (
	"context"
	// #nosec G505 -- import de sha1 p/ hash de conteudo (dedup/oshash), nao cripto de seguranca
	"crypto/sha1"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/localcache"
	"github.com/lgldsilva/jackui/internal/localstream"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

const (
	ErrMissingMountOrPathParam = "missing mount or path parameter"
	mountMeusDownloads         = "meus downloads"
	errOnlyMeusDownloads       = "Somente a área 'Meus downloads' pode ser modificada ou promovida"
	errFileOrDirNotFound       = "file or directory not found"
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

func CheckMountAccess(b *lb.Browser, c *gin.Context, mountName string) bool {
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

// ScopePath returns the path scoped to the user's subdir for UserSubpath mounts.
// For regular mounts, returns relPath unchanged.
func ScopePath(b *lb.Browser, c *gin.Context, mountName, relPath string) string {
	return b.UserScopedPath(mountName, relPath, scopeUser(c))
}

// LocalMounts handles GET /api/local/mounts -> []Mount
func LocalMounts(b *lb.Browser) gin.HandlerFunc {
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
func LocalList(b *lb.Browser, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount parameter"})
			return
		}
		if !CheckMountAccess(b, c, mount) {
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

func listHandleError(b *lb.Browser, c *gin.Context, err error) {
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
		c.JSON(http.StatusOK, []lb.Entry{})
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "path not found"})
}

// LocalFile handles GET /api/local/file?mount=NAME&path=REL/FILE
// Serves via http.ServeContent over a metered, read-ahead Session (Range
// requests preserved) so the UI can report transfer speed and rclone/Drive
// mounts get aligned reads. reg may be nil (falls back to a plain stream).
func LocalFile(b *lb.Browser, reg *localstream.Registry, cache *localcache.Cache) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}

		scoped := ScopePath(b, c, mount, path)
		abs, err := b.ResolvePath(mount, scoped)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// Prefer the locally-cached copy when ready: fast disk, no rclone EIO.
		abs = cachedAbs(cache, mount, scoped, abs)

		if !StatLocalFile(c, abs) {
			return
		}

		SetLocalFileSecurityHeaders(c, path)
		serveLocalFileMetered(c, reg, mount, scoped, abs)
	}
}

// serveLocalFileMetered serves abs with Range/HEAD support. Remote/FUSE mounts
// (rclone/Drive/NFS) go through a metered read-ahead Session: high-latency
// round-trips are amortized by big aligned reads and the UI gets a transfer
// speed for /local/transfer-status. Local-disk files are served directly via
// http.ServeFile — the kernel page cache + sendfile already give instant Range
// seeks, whereas the 16 MB synchronous read-ahead would add ~1s of first-byte
// latency to EVERY seek (slow scrubbing on downloaded videos). reg==nil (older
// tests) also serves directly. A file cached from a remote mount is by now on
// local disk, so isRemoteFS(abs) is false → it serves fast too.
func serveLocalFileMetered(c *gin.Context, reg *localstream.Registry, mount, scoped, abs string) {
	if reg == nil || !isRemoteFS(abs) {
		http.ServeFile(c.Writer, c.Request, abs)
		return
	}
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
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
	// already forced by SetLocalFileSecurityHeaders) and honours Range/HEAD.
	http.ServeContent(c.Writer, c.Request, filepath.Base(abs), st.ModTime(), sess)
}

// StatLocalFile stats abs and writes the appropriate JSON error (returning
// false) when it's missing, unreadable, or a directory. Returns true when abs
// is a regular file ready to be served.
func StatLocalFile(c *gin.Context, abs string) bool {
	stat, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": httpshared.ErrFileNotFound})
			return false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	if stat.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": httpshared.ErrPathIsDir})
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

// SetLocalFileSecurityHeaders applies the stored-XSS guard for files served
// same-origin (where the JWT lives in localStorage). MIME sniffing is always
// disabled. Subtitles get the correct text/vtt so <track> renders them;
// actively rendered formats (html/svg/xml/js) are forced to download instead of
// executing in our origin. Media gets an explicit Content-Type (see
// localMediaContentType) since the image lacks /etc/mime.types.
func SetLocalFileSecurityHeaders(c *gin.Context, path string) {
	c.Header("X-Content-Type-Options", "nosniff")
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".vtt", ".srt":
		c.Header(httpshared.ContentType, "text/vtt; charset=utf-8")
	case ".html", ".htm", ".xhtml", ".svg", ".xml", ".js", ".mjs":
		c.Header(httpshared.ContentType, "application/octet-stream")
		c.Header("Content-Disposition", "attachment; filename=\""+filepath.Base(path)+"\"")
	default:
		if ct := localMediaContentType[ext]; ct != "" {
			c.Header(httpshared.ContentType, ct)
		}
	}
}

// localVideoExts mirrors the frontend's video detection — only these get a
// frame preview (ffmpeg on a non-video would just fail/waste work).
var localVideoExts = map[string]bool{
	".mp4": true, ".m4v": true, ".mkv": true, ".avi": true, ".mov": true, ".wmv": true,
	".webm": true, ".flv": true, ".mpeg": true, ".mpg": true, ".ts": true, ".m2ts": true,
}

func LocalThumb(b *lb.Browser) gin.HandlerFunc {
	return func(c *gin.Context) { localThumbHandler(b, c) }
}

func localThumbHandler(b *lb.Browser, c *gin.Context) {
	mount := c.Query("mount")
	path := c.Query("path")
	if mount == "" || path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
		return
	}
	if !CheckMountAccess(b, c, mount) {
		return
	}
	if !localVideoExts[strings.ToLower(filepath.Ext(path))] {
		c.Status(http.StatusNoContent)
		return
	}
	abs, err := resolveLocalAbs(b, mount, ScopePath(b, c, mount, path))
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
	c.Header(httpshared.CacheControl, httpshared.CachePublicDay)
	c.Data(http.StatusOK, httpshared.MIMEJPEG, out)
}

func resolveLocalAbs(b *lb.Browser, mount, path string) (string, error) {
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
	// #nosec G401 -- sha1/md5 p/ hash de conteudo (dedup/oshash), nao uso criptografico de seguranca
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
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return false
	}
	c.Header(httpshared.CacheControl, httpshared.CachePublicDay)
	c.Data(http.StatusOK, httpshared.MIMEJPEG, data)
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
		// #nosec G204 -- binario fixo/de config; valores de usuario sao operandos de -i ou inteiros; exec sem shell
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
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	if os.MkdirAll(cacheDir, 0o755) != nil {
		return out
	}
	if len(out) > 0 {
		// #nosec G306 -- arquivo de midia/cache; 0644 intencional p/ leitura
		_ = os.WriteFile(cachePath, out, 0o644)
		_ = os.Remove(negativeMarkerPath(cachePath)) // a success clears any stale failure
		return out
	}
	// Persist the FAILURE so repeated listings don't re-run a ~12s decode for a
	// frame ffmpeg can't produce — but only on a real ffmpeg error/timeout, not
	// when the client cancelled (navigated away) before we finished.
	if ctx.Err() != context.Canceled {
		// #nosec G306 -- arquivo de midia/cache; 0644 intencional p/ leitura
		_ = os.WriteFile(negativeMarkerPath(cachePath), nil, 0o644)
	}
	return out
}

// LocalTranscode handles GET /api/local/transcode?mount=NAME&path=REL
// Transcodes a local file to H.264/AAC fragmented MP4 so browsers can play
// formats like MKV, AVI or any container not natively supported.
func LocalTranscode(b *lb.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}
		abs, err := b.ResolvePath(mount, ScopePath(b, c, mount, path))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		stat, err := os.Stat(abs)
		if err != nil || stat.IsDir() {
			c.JSON(http.StatusNotFound, gin.H{"error": httpshared.ErrFileNotFound})
			return
		}
		// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
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

func LocalDelete(b *lb.Browser, dls *downloads.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}
		if !canModifyMount(c, mount) {
			return
		}
		abs, err := resolveDeletablePath(b, mount, ScopePath(b, c, mount, path))
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": errFileOrDirNotFound})
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
func LocalCleanEmptyDirs(b *lb.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		if mount == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}
		if !canModifyMount(c, mount) {
			return
		}
		cleaned, err := b.RemoveEmptyDirs(mount, ScopePath(b, c, mount, c.Query("path")))
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
func LocalSetFolderLock(b *lb.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req folderLockReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.Mount == "" || req.Path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
			return
		}
		if !CheckMountAccess(b, c, req.Mount) {
			return
		}
		if !canModifyMount(c, req.Mount) {
			return
		}
		if err := b.SetFolderLock(req.Mount, ScopePath(b, c, req.Mount, req.Path), req.Locked); err != nil {
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

func resolveDeletablePath(b *lb.Browser, mount, path string) (string, error) {
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

func isMountRoot(b *lb.Browser, abs string) bool {
	for _, m := range b.Mounts() {
		mountAbs, err := filepath.Abs(m.Path)
		if err == nil && abs == mountAbs {
			return true
		}
	}
	return false
}

// LocalWalk handles GET /api/local/walk?mount=&path=&media_only=
// Recursively lists all files under a directory in a mount.
func LocalWalk(b *lb.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPath})
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}
		mediaOnly := c.Query("media_only") == "true" || c.Query("media_only") == "1"
		entries, err := b.Walk(mount, ScopePath(b, c, mount, path), mediaOnly)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "path not found"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if entries == nil {
			entries = []lb.Entry{}
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
