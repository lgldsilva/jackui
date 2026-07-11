package local

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
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
