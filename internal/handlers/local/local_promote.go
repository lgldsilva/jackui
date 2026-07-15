package local

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/renamer"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/tmdb"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// Promote (mover arquivos locais pra biblioteca) + preview — extraído de local.go.
// LocalPromoteDeps bundles the dependencies of the LocalPromote handler so its
// factory stays within the ≤7-parameter limit (S107). Injected by cmd/server.
type LocalPromoteDeps struct {
	Browser    *lb.Browser
	AIClient   *ai.Client
	TMDBClient *tmdb.Client
	SharedDir  string
	Dests      []httpshared.PromoteDest
	Downloads  *downloads.Store
	Streamer   *streamer.Streamer
	Tracker    *transfer.Tracker
}

func LocalPromote(d LocalPromoteDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.SharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": httpshared.ErrSharedDirNotConfig})
			return
		}
		req, base, ok := extractLocalPromoteReq(c, d.Browser, d.SharedDir, d.Dests)
		if !ok {
			return
		}
		if abortIfPromotePathsHidden(c, d.Streamer, req) {
			return
		}
		targetDir, err := localPromoteTargetDir(base, req.TargetSubdir)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		username := scopeUser(c)
		orig := originalLocalPaths(req)
		paths := resolveLocalPaths(d.Browser, req, username)
		if len(paths) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "nenhum arquivo para promover"})
			return
		}
		deps := &promoteDstDeps{
			ctx: c.Request.Context(), aiClient: d.AIClient, tmdbClient: d.TMDBClient,
			base: base, mount: req.Mount, dls: d.Downloads, s: d.Streamer,
			overrides: scopedOverrides(d.Browser, req, username),
		}
		// One Transfers job for the whole batch (X/Y files across all items) — the
		// dock shows live progress while this (possibly large/cross-fs) move runs.
		userID, _, _ := auth.UserIDFromCtx(c)
		deps.job = startPromoteJob(d.Tracker, d.Browser, req, paths, userID)
		moved, errs, results := execPromoteMoves(d.Browser, deps, req.Mount, paths, orig, targetDir)
		deps.job.Done()
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
	subdir, err := httpshared.SanitizeSubdir(subdirStr)
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
func execPromoteMoves(b *lb.Browser, deps *promoteDstDeps, mount string, paths, orig []string, targetDir string) (int, []gin.H, []gin.H) {
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

// startPromoteJob opens a single Transfers job covering every path in the batch
// (X/Y files + summed bytes), so the dock shows aggregate progress. nil tracker
// → nil job (all reporting becomes a no-op).
func startPromoteJob(tr *transfer.Tracker, b *lb.Browser, req *localPromoteReq, paths []string, userID int) *transfer.Job {
	files, total := 0, int64(0)
	for _, rel := range paths {
		if abs, err := b.ResolvePath(req.Mount, rel); err == nil {
			f, by := CountTree(abs)
			files += f
			total += by
		}
	}
	label := promoteJobLabel(req, paths)
	return tr.StartFor(userID, label, "promote", files, total)
}

// promoteJobLabel names the dock entry: the single file's name, or "N itens".
func promoteJobLabel(req *localPromoteReq, paths []string) string {
	if len(paths) == 1 {
		return filepath.Base(paths[0])
	}
	if req.Path != "" {
		return filepath.Base(req.Path)
	}
	return fmt.Sprintf("%d itens", len(paths))
}

// promoteOnePath moves one already-scoped relative path into targetDir, applying
// the AI rename via computePromoteDst. Returns nil on success (incl. a no-op when
// already in place) or a {path,error} map describing the failure.
func promoteOnePath(b *lb.Browser, deps *promoteDstDeps, mount, scopedRel, targetDir string) gin.H {
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
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return gin.H{"path": scopedRel, "error": "criar destino: " + err.Error()}
	}
	files, bytes := CountTree(src)
	if err := MovePathJob(src, dst, stat, deps.job, files, bytes); err != nil {
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
//
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
	folders := httpshared.ListDirs(entries)
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

func LocalPromotePreview(b *lb.Browser, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []httpshared.PromoteDest, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": httpshared.ErrSharedDirNotConfig})
			return
		}
		req, base, ok := extractLocalPromoteReq(c, b, sharedDir, dests)
		if !ok {
			return
		}
		if abortIfPromotePathsHidden(c, s, req) {
			return
		}
		orig := originalLocalPaths(req)
		paths := resolveLocalPaths(b, req, scopeUser(c))
		previews := buildLocalPreviews(&localPreviewDeps{c: c, b: b, aiClient: aiClient, tmdbClient: tmdbClient, mount: req.Mount, base: base}, paths, orig)
		c.JSON(http.StatusOK, gin.H{"previews": previews})
	}
}

// abortIfPromotePathsHidden refuses promote/preview when any source path is
// behind the closed curtain (single Path and multi Paths).
func abortIfPromotePathsHidden(c *gin.Context, s *streamer.Streamer, req *localPromoteReq) bool {
	if req == nil {
		return false
	}
	if AbortIfLocalPathHidden(c, s, req.Mount, req.Path) {
		return true
	}
	for _, p := range req.Paths {
		if AbortIfLocalPathHidden(c, s, req.Mount, p) {
			return true
		}
	}
	return false
}

type localPreviewDeps struct {
	c          *gin.Context
	b          *lb.Browser
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
	// job reports per-file move progress to the global Transfers dock (nil-safe).
	job *transfer.Job
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

func extractLocalPromoteReq(c *gin.Context, b *lb.Browser, sharedDir string, dests []httpshared.PromoteDest) (*localPromoteReq, string, bool) {
	var req localPromoteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": httpshared.ErrInvalidData})
		return nil, "", false
	}
	if req.Mount == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount parameter"})
		return nil, "", false
	}
	if !CheckMountAccess(b, c, req.Mount) {
		return nil, "", false
	}
	if !canModifyMount(c, req.Mount) {
		return nil, "", false
	}
	base, err := httpshared.ResolveTargetBase(req.TargetBase, sharedDir, dests)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, "", false
	}
	return &req, base, true
}

func resolveLocalPaths(b *lb.Browser, req *localPromoteReq, username string) []string {
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
