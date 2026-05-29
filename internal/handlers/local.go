package handlers

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/ai"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/local"
	"github.com/luizg/jackui/internal/renamer"
	"github.com/luizg/jackui/internal/tmdb"
	"github.com/luizg/jackui/internal/transcode"
)

// userFromCtx extracts the username from the JWT claims, returning ""
// when auth is not enabled or user is anonymous (media tokens).
func userFromCtx(c *gin.Context) string {
	claims, ok := auth.ClaimsFromCtx(c)
	if !ok || claims.Scope == auth.ScopeMedia {
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

// LocalMounts handles GET /api/local/mounts -> []Mount
func LocalMounts(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		username := userFromCtx(c)
		c.JSON(http.StatusOK, b.MountsFor(username))
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

		entries, err := b.List(mount, path)
		if err != nil {
			if isTraversalErr(err) {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "path not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, entries)
	}
}

// LocalFile handles GET /api/local/file?mount=NAME&path=REL/FILE
// Uses http.ServeFile which handles Range requests natively.
func LocalFile(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount or path parameter"})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}

		abs, err := b.ResolvePath(mount, path)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		stat, err := os.Stat(abs)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if stat.IsDir() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "path is a directory"})
			return
		}

		http.ServeFile(c.Writer, c.Request, abs)
	}
}

// localVideoExts mirrors the frontend's video detection — only these get a
// frame preview (ffmpeg on a non-video would just fail/waste work).
var localVideoExts = map[string]bool{
	".mp4": true, ".m4v": true, ".mkv": true, ".avi": true, ".mov": true, ".wmv": true,
	".webm": true, ".flv": true, ".mpeg": true, ".mpg": true, ".ts": true, ".m2ts": true,
}

// LocalThumb handles GET /api/local/thumb?mount=NAME&path=REL&at=SECONDS —
// extracts a single early frame from a local video file as JPEG, cached on disk.
// Used by the file browser to show a preview instead of a generic icon. Loaded
// by <img>, so it accepts ?token= (see isMediaPath).
func LocalThumb(b *local.Browser) gin.HandlerFunc {
	cacheDir := filepath.Join(os.TempDir(), "jackui-local-thumbs")
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount or path parameter"})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		if !localVideoExts[strings.ToLower(filepath.Ext(path))] {
			c.Status(http.StatusNoContent)
			return
		}
		abs, err := b.ResolvePath(mount, path)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		stat, err := os.Stat(abs)
		if err != nil || stat.IsDir() {
			c.Status(http.StatusNotFound)
			return
		}
		at := 10
		if v, e := strconv.Atoi(c.Query("at")); e == nil && v >= 0 {
			at = v
		}

		// Cache key includes mod time so editing/replacing the file busts it.
		key := fmt.Sprintf("%x", sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", abs, stat.ModTime().UnixNano(), at))))
		cachePath := filepath.Join(cacheDir, key+".jpg")
		if data, rerr := os.ReadFile(cachePath); rerr == nil {
			c.Header("Cache-Control", "public, max-age=86400")
			c.Data(http.StatusOK, "image/jpeg", data)
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		// Try the requested timestamp first, then 1s — a short clip may have no
		// frame at `at`, and the very start is sometimes black/garbage.
		seeks := []int{at}
		if at != 1 {
			seeks = append(seeks, 1)
		}
		var out []byte
		for _, s := range seeks {
			cmd := exec.CommandContext(ctx, "ffmpeg",
				"-hide_banner", "-loglevel", "error",
				"-ss", strconv.Itoa(s),
				"-i", abs,
				"-frames:v", "1",
				"-vf", "scale=320:-2",
				"-q:v", "5",
				"-f", "mjpeg",
				"-y", "pipe:1",
			)
			if data, cerr := cmd.Output(); cerr == nil && len(data) > 0 {
				out = data
				break
			}
		}
		if len(out) == 0 {
			c.Status(http.StatusNoContent)
			return
		}
		if os.MkdirAll(cacheDir, 0o755) == nil {
			_ = os.WriteFile(cachePath, out, 0o644)
		}
		c.Header("Cache-Control", "public, max-age=86400")
		c.Data(http.StatusOK, "image/jpeg", out)
	}
}

// LocalTranscode handles GET /api/local/transcode?mount=NAME&path=REL
// Transcodes a local file to H.264/AAC fragmented MP4 so browsers can play
// formats like MKV, AVI or any container not natively supported.
func LocalTranscode(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount or path parameter"})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		abs, err := b.ResolvePath(mount, path)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		stat, err := os.Stat(abs)
		if err != nil || stat.IsDir() {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}
		f, err := os.Open(abs)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer func() { _ = f.Close() }()

		opts := transcode.Options{
			AudioTrack:  -1,
			SubBurnTrack: -1,
			VideoCodec:  "h264",
			AudioCodec:  "aac",
			Container:   "mp4",
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

// LocalDelete handles DELETE /api/local/file?mount=NAME&path=REL
func LocalDelete(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount or path parameter"})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}

		// Admin pode modificar qualquer mount; não-admin só "Meus downloads"
		claims, _ := auth.ClaimsFromCtx(c)
		isAdmin := claims != nil && claims.Role == auth.RoleAdmin
		if !isAdmin && strings.ToLower(mount) != "meus downloads" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Somente a área 'Meus downloads' pode ser modificada ou promovida"})
			return
		}

		// Prevent deleting mount root
		cleanPath := filepath.Clean(path)
		if cleanPath == "" || cleanPath == "." || cleanPath == "/" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete mount root"})
			return
		}

		abs, err := b.ResolvePath(mount, path)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Double safety check: make sure absolute path is not the mount root itself
		mounts := b.Mounts()
		for _, m := range mounts {
			mountAbs, err := filepath.Abs(m.Path)
			if err == nil && abs == mountAbs {
				c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete mount root"})
				return
			}
		}

		_, err = os.Stat(abs)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "file or directory not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Perform deletion
		if err := os.RemoveAll(abs); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to delete: %s", err.Error())})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "deleted successfully"})
	}
}

type localPromoteReq struct {
	Mount        string   `json:"mount"`
	Path         string   `json:"path"`
	Paths        []string `json:"paths"`
	TargetSubdir string   `json:"targetSubdir"`
	TargetBase   string   `json:"targetBase"` // empty = sharedDir (default)
	RenameIA     bool     `json:"renameIA"`
}

// LocalPromote handles POST /api/local/promote
func LocalPromote(b *local.Browser, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		_, src, base, subdir, ok := validatePromote(c, b, sharedDir, dests)
		if !ok {
			return
		}

		targetDir := base
		if subdir != "" {
			targetDir = filepath.Join(base, subdir)
		}

		baseName := filepath.Base(src)
		dst, targetDir := computePromoteDst(c.Request.Context(), aiClient, tmdbClient, baseName, base, targetDir)

		if src == dst {
			c.JSON(http.StatusOK, gin.H{"message": "source and destination are the same", "path": dst})
			return
		}

		stat, statErr := os.Stat(src)
		if statErr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "arquivo de origem não existe: " + statErr.Error()})
			return
		}

		if err := os.MkdirAll(targetDir, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "criar destino: " + err.Error()})
			return
		}

		if err := movePath(src, dst, stat); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "mover arquivo: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "promoted successfully", "path": dst})
	}
}

func validatePromote(c *gin.Context, b *local.Browser, sharedDir string, dests []PromoteDest) (*localPromoteReq, string, string, string, bool) {
	if sharedDir == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "JACKUI_SHARED_DIR não configurado"})
		return nil, "", "", "", false
	}

	var req localPromoteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dados inválidos"})
		return nil, "", "", "", false
	}

	if req.Mount == "" || req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount or path parameter"})
		return nil, "", "", "", false
	}
	if !checkMountAccess(b, c, req.Mount) {
		return nil, "", "", "", false
	}

	claims, _ := auth.ClaimsFromCtx(c)
	isAdmin := claims != nil && claims.Role == auth.RoleAdmin
	if !isAdmin && strings.ToLower(req.Mount) != "meus downloads" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Somente a área 'Meus downloads' pode ser modificada ou promovida"})
		return nil, "", "", "", false
	}

	cleanPath := filepath.Clean(req.Path)
	if cleanPath == "" || cleanPath == "." || cleanPath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot promote mount root"})
		return nil, "", "", "", false
	}

	src, err := b.ResolvePath(req.Mount, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, "", "", "", false
	}

	base, err := resolveTargetBase(req.TargetBase, sharedDir, dests)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, "", "", "", false
	}

	subdir, err := sanitizeSubdir(req.TargetSubdir)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, "", "", "", false
	}

	return &req, src, base, subdir, true
}

func computePromoteDst(ctx context.Context, aiClient *ai.Client, tmdbClient *tmdb.Client, baseName, base, targetDir string) (string, string) {
	if aiClient != nil {
		preview, err := renamer.GeneratePreview(ctx, aiClient, tmdbClient, baseName)
		if err == nil && preview != nil {
			targetRel := renamer.ResolveTargetConflict(base, preview.TargetPath)
			dst := filepath.Join(base, targetRel)
			return dst, filepath.Dir(dst)
		}
	}
	return filepath.Join(targetDir, baseName), targetDir
}

// LocalPromotePreview handles POST /api/local/promote/preview
func LocalPromotePreview(b *local.Browser, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": "JACKUI_SHARED_DIR não configurado"})
			return
		}

		var req localPromoteReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "dados inválidos"})
			return
		}

		if req.Mount == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount parameter"})
			return
		}

		if !checkMountAccess(b, c, req.Mount) {
			return
		}

		// Admin pode modificar qualquer mount; não-admin só "Meus downloads"
		claims, _ := auth.ClaimsFromCtx(c)
		isAdmin := claims != nil && claims.Role == auth.RoleAdmin
		if !isAdmin && strings.ToLower(req.Mount) != "meus downloads" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Somente a área 'Meus downloads' pode ser modificada ou promovida"})
			return
		}

		base, err := resolveTargetBase(req.TargetBase, sharedDir, dests)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		paths := req.Paths
		if len(paths) == 0 && req.Path != "" {
			paths = []string{req.Path}
		}

		if len(paths) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "paths vazio"})
			return
		}

		previews := []gin.H{}
		for _, p := range paths {
			previews = append(previews, previewItem(c, b, aiClient, tmdbClient, req.Mount, p, base))
		}

		c.JSON(http.StatusOK, gin.H{"previews": previews})
	}
}

func previewItem(c *gin.Context, b *local.Browser, aiClient *ai.Client, tmdbClient *tmdb.Client, mount, p, base string) gin.H {
	cleanPath := filepath.Clean(p)
	if cleanPath == "" || cleanPath == "." || cleanPath == "/" {
		return gin.H{"path": p, "error": "cannot promote mount root"}
	}

	src, err := b.ResolvePath(mount, p)
	if err != nil {
		return gin.H{"path": p, "error": err.Error()}
	}

	if _, err := os.Stat(src); err != nil {
		return gin.H{"path": p, "error": "arquivo não existe"}
	}

	baseName := filepath.Base(src)
	preview, err := renamer.GeneratePreview(c.Request.Context(), aiClient, tmdbClient, baseName)
	if err != nil {
		return gin.H{"path": p, "error": err.Error()}
	}

	nonConflicting := renamer.ResolveTargetConflict(base, preview.TargetPath)
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount or path"})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		mediaOnly := c.Query("media_only") == "true" || c.Query("media_only") == "1"
		entries, err := b.Walk(mount, path, mediaOnly)
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

// LocalMoveEntry handles POST /api/local/move — moves a file or directory
// from one mount to another (or within the same mount). Admin only.
// Body: { srcMount, srcPath, dstMount, dstPath (target directory) }
func LocalMoveEntry(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			SrcMount string `json:"srcMount"`
			SrcPath  string `json:"srcPath"`
			DstMount string `json:"dstMount"`
			DstPath  string `json:"dstPath"` // target directory (entry name preserved)
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.SrcMount == "" || req.SrcPath == "" || req.DstMount == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "srcMount, srcPath and dstMount are required"})
			return
		}

		claims, _ := auth.ClaimsFromCtx(c)
		if claims == nil || claims.Role != auth.RoleAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "apenas admins podem mover entre mounts"})
			return
		}
		if !checkMountAccess(b, c, req.SrcMount) {
			return
		}
		if !checkMountAccess(b, c, req.DstMount) {
			return
		}

		srcAbs, err := b.ResolvePath(req.SrcMount, req.SrcPath)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "origem: " + err.Error()})
			return
		}
		srcStat, err := os.Stat(srcAbs)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "origem não encontrada"})
			return
		}

		// Destination: resolve target directory, then append the source basename.
		dstDirRel := req.DstPath
		if dstDirRel == "" {
			dstDirRel = "."
		}
		dstDirAbs, err := b.ResolvePath(req.DstMount, dstDirRel)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "destino: " + err.Error()})
			return
		}
		dstAbs := filepath.Join(dstDirAbs, filepath.Base(srcAbs))

		// Refuse to move a directory into itself.
		if srcStat.IsDir() && strings.HasPrefix(dstAbs+string(filepath.Separator), srcAbs+string(filepath.Separator)) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "não é possível mover uma pasta para dentro de si mesma"})
			return
		}

		if err := os.MkdirAll(dstDirAbs, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "criar diretório destino: " + err.Error()})
			return
		}
		if err := movePath(srcAbs, dstAbs, srcStat); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "mover: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"moved": filepath.Join(req.DstMount, req.DstPath, filepath.Base(req.SrcPath))})
	}
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

	// After recursive copying, remove the source directory completely
	return os.RemoveAll(src)
}
