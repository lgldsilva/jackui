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
	"github.com/luizg/jackui/internal/ai"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/local"
	"github.com/luizg/jackui/internal/renamer"
	"github.com/luizg/jackui/internal/streamer"
	"github.com/luizg/jackui/internal/tmdb"
	"github.com/luizg/jackui/internal/transcode"
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

// LocalList handles GET /api/local/list?mount=NAME&path=REL -> []Entry
func LocalList(b *local.Browser) gin.HandlerFunc {
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
// Uses http.ServeFile which handles Range requests natively.
func LocalFile(b *local.Browser) gin.HandlerFunc {
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

		if !statLocalFile(c, abs) {
			return
		}

		setLocalFileSecurityHeaders(c, path)
		http.ServeFile(c.Writer, c.Request, abs)
	}
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

// setLocalFileSecurityHeaders applies the stored-XSS guard for files served
// same-origin (where the JWT lives in localStorage). MIME sniffing is always
// disabled. Subtitles get the correct text/vtt so <track> renders them;
// actively rendered formats (html/svg/xml/js) are forced to download instead
// of executing in our origin. Media (video/audio) keeps ServeFile's type.
func setLocalFileSecurityHeaders(c *gin.Context, path string) {
	c.Header("X-Content-Type-Options", "nosniff")
	switch strings.ToLower(filepath.Ext(path)) {
	case ".vtt", ".srt":
		c.Header("Content-Type", "text/vtt; charset=utf-8")
	case ".html", ".htm", ".xhtml", ".svg", ".xml", ".js", ".mjs":
		c.Header("Content-Type", "application/octet-stream")
		c.Header("Content-Disposition", "attachment; filename=\""+filepath.Base(path)+"\"")
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

func LocalPromote(b *local.Browser, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []PromoteDest) gin.HandlerFunc {
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
		paths := resolveLocalPaths(b, req, scopeUser(c))
		if len(paths) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "nenhum arquivo para promover"})
			return
		}
		deps := &promoteDstDeps{ctx: c.Request.Context(), aiClient: aiClient, tmdbClient: tmdbClient, base: base}
		moved, errs := execPromoteMoves(b, deps, req.Mount, paths, targetDir)
		if moved == 0 {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"moved": 0, "failed": len(errs), "errors": errs})
			return
		}
		c.JSON(http.StatusOK, gin.H{"moved": moved, "failed": len(errs), "errors": errs})
	}
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

func execPromoteMoves(b *local.Browser, deps *promoteDstDeps, mount string, paths []string, targetDir string) (int, []gin.H) {
	moved := 0
	errs := make([]gin.H, 0)
	for _, scopedRel := range paths {
		if e := promoteOnePath(b, deps, mount, scopedRel, targetDir); e != nil {
			errs = append(errs, e)
		} else {
			moved++
		}
	}
	return moved, errs
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
	dst, dir := computePromoteDst(deps, baseName, targetDir)
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
	return nil
}

func computePromoteDst(d *promoteDstDeps, baseName, targetDir string) (string, string) {
	if d.aiClient != nil {
		preview, err := renamer.GeneratePreview(d.ctx, d.aiClient, d.tmdbClient, baseName)
		if err == nil && preview != nil {
			targetRel := renamer.ResolveTargetConflict(d.base, preview.TargetPath)
			dst := filepath.Join(d.base, targetRel)
			return dst, filepath.Dir(dst)
		}
	}
	return filepath.Join(targetDir, baseName), targetDir
}

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
		paths := resolveLocalPaths(b, req, scopeUser(c))
		previews := buildLocalPreviews(&localPreviewDeps{c: c, b: b, aiClient: aiClient, tmdbClient: tmdbClient, mount: req.Mount, base: base}, paths)
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
}

type localPromoteReq struct {
	Mount        string   `json:"mount"`
	Path         string   `json:"path"`
	Paths        []string `json:"paths"`
	TargetSubdir string   `json:"targetSubdir"`
	TargetBase   string   `json:"targetBase"`
	RenameIA     bool     `json:"renameIA"`
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

func buildLocalPreviews(d *localPreviewDeps, paths []string) []gin.H {
	if len(paths) == 0 {
		return []gin.H{}
	}
	previews := make([]gin.H, 0, len(paths))
	for _, p := range paths {
		previews = append(previews, previewItem(d, p))
	}
	return previews
}

func previewItem(d *localPreviewDeps, p string) gin.H {
	cleanPath := filepath.Clean(p)
	if cleanPath == "" || cleanPath == "." || cleanPath == "/" {
		return gin.H{"path": p, "error": "cannot promote mount root"}
	}

	src, err := d.b.ResolvePath(d.mount, p)
	if err != nil {
		return gin.H{"path": p, "error": err.Error()}
	}

	if _, err := os.Stat(src); err != nil {
		return gin.H{"path": p, "error": "arquivo não existe"}
	}

	baseName := filepath.Base(src)
	preview, err := renamer.GeneratePreview(d.c.Request.Context(), d.aiClient, d.tmdbClient, baseName)
	if err != nil {
		return gin.H{"path": p, "error": err.Error()}
	}

	nonConflicting := renamer.ResolveTargetConflict(d.base, preview.TargetPath)
	return gin.H{
		"path":         p,
		"originalName": baseName,
		"cleanName":    preview.CleanName,
		"targetPath":   nonConflicting,
		"kind":         preview.Kind,
		"year":         preview.Year,
		"season":       preview.Season,
		"episode":      preview.Episode,
		"episodeName":  preview.EpisodeName,
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
func LocalMoveEntry(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		localMoveHandler(c, b)
	}
}

func localMoveHandler(c *gin.Context, b *local.Browser) {
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
	c.JSON(http.StatusOK, gin.H{"moved": filepath.Join(req.DstMount, req.DstPath, filepath.Base(req.SrcPath))})
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
