package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/diskutil"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	lh "github.com/lgldsilva/jackui/internal/handlers/local"
	"github.com/lgldsilva/jackui/internal/renamer"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/tmdb"
	"github.com/lgldsilva/jackui/internal/transfer"
)

const (
	errDownloadNotFound = "download not found"
)

// promoteReq is the body of POST /api/downloads/:id/promote and of the batch handler.
// targetSubdir is relative to sharedDir; validated against path traversal.
type promoteReq struct {
	KeepSeeding  bool   `json:"keepSeeding"`
	TargetSubdir string `json:"targetSubdir"`
	TargetBase   string `json:"targetBase"` // empty = sharedDir (default)
	RenameIA     bool   `json:"renameIA"`
	// Batch only:
	IDs []int `json:"ids"`
}

// PromoteDeps bundles the shared dependencies of the promote handlers
// (DownloadsPromote/DownloadsPromoteBatch). Passed as one struct so the handler
// factories stay within the ≤7-parameter limit (S107) — the wiring in cmd/server
// injects it, mirroring the existing promoteOpts/previewDeps pattern.
type PromoteDeps struct {
	Store      *downloads.Store
	Streamer   *streamer.Streamer
	AIClient   *ai.Client
	TMDBClient *tmdb.Client
	SharedDir  string
	Dests      []httpshared.PromoteDest
	Tracker    *transfer.Tracker
	Pending    *transfer.Store
	Cfg        *config.Config
}

// BuildPromoteDests returns the full list of promote destinations: sharedDir
// is always first ("Biblioteca"), followed by any configured extras.
func BuildPromoteDests(sharedDir string, extra []httpshared.PromoteDest) []httpshared.PromoteDest {
	dests := []httpshared.PromoteDest{}
	if sharedDir != "" {
		dests = append(dests, httpshared.PromoteDest{Name: "Biblioteca", Path: sharedDir})
	}
	dests = append(dests, extra...)
	return dests
}

// DownloadsPromote handles POST /api/downloads/:id/promote — moves a completed
// download to sharedDir (optionally into targetSubdir). Body:
//
//	{ "keepSeeding": bool, "targetSubdir": "movies/2026", "targetBase": "/mnt/gdrive/media" }
//
// Empty targetSubdir = destination root. Empty targetBase = sharedDir (default).
// Missing subfolders are created (os.MkdirAll). Anti-traversal validation.
func DownloadsPromote(d PromoteDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		if d.SharedDir == "" {
			httpshared.RespondErrorMessage(c, http.StatusConflict, httpshared.ErrSharedDirNotConfig)
			return
		}
		var req promoteReq
		_ = c.ShouldBindJSON(&req)

		base, err := httpshared.ResolveTargetBase(req.TargetBase, d.SharedDir, d.Dests)
		if err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}

		userID, _, _ := auth.UserIDFromCtx(c)
		o := &promoteOpts{store: d.Store, s: d.Streamer, aiClient: d.AIClient, tmdbClient: d.TMDBClient, sharedDir: base, userID: userID, id: id, targetSubdir: req.TargetSubdir, keepSeeding: req.KeepSeeding, renameIA: req.RenameIA, tracker: d.Tracker, pending: d.Pending, concMode: transferMode(d.Cfg)}
		plan, err := promotePreparePlan(o)
		if err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		if plan != nil {
			// Copy runs in the background (transfer pool) → reply immediately.
			submitPromotePlans(o, d.Tracker, []*promotePlan{plan})
		}
		// Optimistic return: file_path still points at the original until the copy
		// finishes (the list reflects the destination once the dock job completes).
		updated, _ := d.Store.Get(userID, id)
		c.JSON(http.StatusOK, updated)
	}
}

// DownloadsPromoteBatch handles POST /api/downloads/promote — promotes a list
// of downloads to the SAME targetSubdir. Body:
//
//	{ "ids": [1,2,3], "targetSubdir": "movies", "keepSeeding": false, "targetBase": "/mnt/gdrive/media" }
//
// Response: { "promoted": [<DownloadEntry>...], "failed": [{id, error}...] }
// Individual failures do not abort the batch — each item is attempted.
func DownloadsPromoteBatch(d PromoteDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.SharedDir == "" {
			httpshared.RespondErrorMessage(c, http.StatusConflict, httpshared.ErrSharedDirNotConfig)
			return
		}
		req, base, ok := validateBatchReq(c, d.SharedDir, d.Dests)
		if !ok {
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		promoted, failed := promoteBatchItems(&promoteOpts{store: d.Store, s: d.Streamer, aiClient: d.AIClient, tmdbClient: d.TMDBClient, sharedDir: base, userID: userID, targetSubdir: req.TargetSubdir, keepSeeding: req.KeepSeeding, renameIA: req.RenameIA, tracker: d.Tracker, pending: d.Pending, concMode: transferMode(d.Cfg)}, req, d.Tracker)
		c.JSON(http.StatusOK, gin.H{"promoted": promoted, "failed": failed})
	}
}

func validateBatchReq(c *gin.Context, sharedDir string, dests []httpshared.PromoteDest) (*promoteReq, string, bool) {
	var req promoteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpshared.RespondError(c, http.StatusBadRequest, err)
		return nil, "", false
	}
	if len(req.IDs) == 0 {
		httpshared.RespondErrorMessage(c, http.StatusBadRequest, "empty ids")
		return nil, "", false
	}
	base, err := httpshared.ResolveTargetBase(req.TargetBase, sharedDir, dests)
	if err != nil {
		httpshared.RespondError(c, http.StatusBadRequest, err)
		return nil, "", false
	}
	return &req, base, true
}

// promoteBatchItems validates each item SYNCHRONOUSLY (errors come back in the
// response) and submits the valid ones for background copying. `promoted` is
// optimistic: it lists the accepted items (the copy runs in the Transfers
// panel); `failed` only carries the ones that failed validation. This keeps the
// response shape and avoids a 504 on a large synchronous copy.
func promoteBatchItems(o *promoteOpts, req *promoteReq, tr *transfer.Tracker) ([]downloads.Download, []gin.H) {
	promoted := []downloads.Download{}
	failed := []gin.H{}
	var plans []*promotePlan
	for _, id := range req.IDs {
		o.id = id
		plan, err := promotePreparePlan(o)
		if err != nil {
			failed = append(failed, gin.H{"id": id, httpshared.ErrorField: err.Error()})
			continue
		}
		if plan == nil { // already at the destination — immediate success, no copy
			if d, _ := o.store.Get(o.userID, id); d != nil {
				promoted = append(promoted, *d)
			}
			continue
		}
		plans = append(plans, plan)
		promoted = append(promoted, *plan.d)
	}
	if len(plans) > 0 {
		submitPromotePlans(o, tr, plans)
	}
	return promoted, failed
}

func DownloadsPromotePreview(store *downloads.Store, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []httpshared.PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			httpshared.RespondErrorMessage(c, http.StatusConflict, httpshared.ErrSharedDirNotConfig)
			return
		}
		var req promoteReq
		if err := c.ShouldBindJSON(&req); err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		if len(req.IDs) == 0 {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "empty ids")
			return
		}
		base, err := httpshared.ResolveTargetBase(req.TargetBase, sharedDir, dests)
		if err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		previews := buildDownloadPreviews(&previewDeps{ctx: c.Request.Context(), store: store, aiClient: aiClient, tmdbClient: tmdbClient, userID: userID, base: base}, req.IDs)
		c.JSON(http.StatusOK, gin.H{"previews": previews})
	}
}

func buildDownloadPreviews(d *previewDeps, ids []int) []gin.H {
	previews := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		previews = append(previews, previewOneDownload(d, id))
	}
	return previews
}

func previewOneDownload(d *previewDeps, id int) gin.H {
	dl, err := d.store.Get(d.userID, id)
	if err != nil || dl == nil {
		return gin.H{"id": id, httpshared.ErrorField: errDownloadNotFound}
	}
	if dl.FilePath == "" {
		return gin.H{"id": id, httpshared.ErrorField: "file_path empty"}
	}
	rawName := filepath.Base(dl.FilePath)
	if rawName == "" || rawName == "." || rawName == "/" {
		rawName = dl.Name
	}
	preview, err := renamer.GeneratePreview(d.ctx, d.aiClient, d.tmdbClient, rawName)
	if err != nil {
		return gin.H{"id": id, httpshared.ErrorField: err.Error()}
	}
	nonConflicting := renamer.ResolveTargetConflict(d.base, preview.TargetPath)
	return gin.H{
		"id":           id,
		"originalName": rawName,
		"cleanName":    preview.CleanName,
		"targetPath":   nonConflicting,
		"kind":         preview.Kind,
		"year":         preview.Year,
		"season":       preview.Season,
		"episode":      preview.Episode,
		"episodeName":  preview.EpisodeName,
	}
}

// DownloadsPromoteBrowse handles GET /api/downloads/promote/browse?path=movies&base=/mnt/gdrive/media
// — lists subfolders under {base}/path to feed the UI browser. Empty
// base = sharedDir. Does not expose files (dirs only).
func DownloadsPromoteBrowse(sharedDir string, dests []httpshared.PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			httpshared.RespondErrorMessage(c, http.StatusConflict, httpshared.ErrSharedDirNotConfig)
			return
		}
		root, err := httpshared.ResolveTargetBase(c.Query("base"), sharedDir, dests)
		if err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		sub, err := httpshared.SanitizeSubdir(c.Query("path"))
		if err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		dir := joinIfSub(root, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"dirs": []string{}, "path": sub})
			return
		}
		c.JSON(http.StatusOK, gin.H{"dirs": httpshared.ListDirs(entries), "path": sub})
	}
}

func joinIfSub(root, sub string) string {
	if sub == "" {
		return root
	}
	return filepath.Join(root, sub)
}

type previewDeps struct {
	ctx        context.Context
	store      *downloads.Store
	aiClient   *ai.Client
	tmdbClient *tmdb.Client
	userID     int
	base       string
}

type promoteOpts struct {
	store        *downloads.Store
	s            *streamer.Streamer
	aiClient     *ai.Client
	tmdbClient   *tmdb.Client
	sharedDir    string
	userID       int
	id           int
	targetSubdir string
	keepSeeding  bool
	renameIA     bool
	tracker      *transfer.Tracker // reports the move to the global Transfers dock (nil-safe)
	pending      *transfer.Store   // persists the copy intent so a restart can resume it (nil-safe)
	concMode     string            // "" / "auto" / "serial" / "parallel" — see TransferConcurrencyMode
}

// Transfer concurrency modes (config TransferConcurrencyMode).
const (
	transferModeAuto     = "auto"
	transferModeSerial   = "serial"
	transferModeParallel = "parallel"
)

// transferMode reads the transfer concurrency mode from the LIVE config (nil-safe → "").
func transferMode(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Stream.TransferConcurrencyMode
}

// shouldSerialize decides, based on mode + destination disk, whether copies run
// one at a time. "auto" (default) detects HDD; "serial"/"parallel" force it.
func shouldSerialize(mode, dst string) bool {
	switch mode {
	case transferModeSerial:
		return true
	case transferModeParallel:
		return false
	default: // "" ou "auto"
		return diskutil.IsRotational(dst)
	}
}

// promotePayload is the kind-specific JSON stored with a pending promote, so the
// boot reconciler can finish the copy AND re-point the download row + re-seed.
type promotePayload struct {
	DownloadID  int  `json:"downloadID"`
	UserID      int  `json:"userID"`
	KeepSeeding bool `json:"keepSeeding"`
}

// promotePlan is a validated promote ready to copy. Validation is synchronous
// (fast); the copy (lh.MovePathJob) runs afterwards in the background — see runPromotePlan.
type promotePlan struct {
	d       *downloads.Download
	src     string
	dst     string
	srcInfo os.FileInfo
	files   int
	bytes   int64
}

// promotePreparePlan validates and resolves the paths SYNCHRONOUSLY (no copy).
// Returns (plan, nil) when there is something to move; (nil, nil) when the file
// is already at the destination (no-op); (nil, err) on validation failure —
// reportable in the immediate response. Separating validation from the copy lets
// the handler reply right away and push the copy (slow for large files) to the
// transfer pool, avoiding the reverse proxy's 504 on a long synchronous copy.
func promotePreparePlan(o *promoteOpts) (*promotePlan, error) {
	d, err := o.store.Get(o.userID, o.id)
	if err != nil || d == nil {
		return nil, errors.New(errDownloadNotFound)
	}
	if d.Status != downloads.StatusCompleted {
		return nil, errors.New("only completed downloads can be promoted")
	}
	if d.FilePath == "" {
		return nil, errors.New("file_path empty — nothing to promote")
	}
	targetDir, err := promoteTargetDir(o)
	if err != nil {
		return nil, err
	}
	src := d.FilePath
	baseName := safeBaseName(src, d.Name)
	dst := promoteDestPath(o, baseName, &targetDir)
	if src == dst {
		return nil, nil // already in place
	}
	srcInfo, statErr := os.Stat(src)
	if statErr != nil {
		return nil, errors.New("source file does not exist: " + statErr.Error())
	}
	if err := ensureTargetDir(targetDir); err != nil {
		return nil, err
	}
	files, bytes := lh.CountTree(src)
	return &promotePlan{d: d, src: src, dst: dst, srcInfo: srcInfo, files: files, bytes: bytes}, nil
}

// runPromotePlan performs the copy + post-processing of a plan. Runs INSIDE the
// transfer job (background); job may be nil (reporting becomes a no-op).
// lh.MovePathJob handles both file AND directory (whole-torrent is a folder).
func runPromotePlan(o *promoteOpts, p *promotePlan, job *transfer.Job) error {
	if err := lh.MovePathJob(p.src, p.dst, p.srcInfo, job, p.files, p.bytes); err != nil {
		return errors.New("move file: " + err.Error())
	}
	_ = o.store.SetFilePath(o.userID, p.d.ID, p.dst)
	applySeedingAfterPromote(o, p.d)
	return nil
}

// submitPromotePlans copies the validated plans in the background. The strategy
// depends on the DESTINATION disk:
//   - HDD (rotational): ONE sequential job. Parallel copies on the same HDD make
//     the disk head seek between them (seek thrashing) and the aggregate
//     throughput plummets versus one copy at a time.
//   - SSD/NVMe: ONE job per item → the pool runs several in parallel, with
//     per-file progress and no seek penalty.
//
// In both cases the intent is persisted BEFORE copying (resume on boot) and
// removed on completion, and the request returns immediately (no 504).
func submitPromotePlans(o *promoteOpts, tr *transfer.Tracker, plans []*promotePlan) {
	if len(plans) == 0 {
		return
	}
	if shouldSerialize(o.concMode, plans[0].dst) {
		submitPromoteSerial(o, tr, plans)
		return
	}
	for _, p := range plans {
		p := p // capture per iteration
		pid := addPendingPromote(o, p)
		tr.SubmitFor(o.userID, safeBaseName(p.src, p.d.Name), "promote", p.files, p.bytes, func(job *transfer.Job) {
			if err := runPromotePlan(o, p, job); err != nil {
				job.Fail(err)
				log.Printf("promote: #%d %q failed: %v", p.d.ID, httpshared.SanitizeForLog(p.d.Name), err)
				return
			}
			_ = o.pending.Remove(pid)
			job.Done()
		})
	}
}

// submitPromoteSerial copies all the plans in a single job, one at a time —
// used when the destination is an HDD (avoids seek thrashing from parallel copies).
func submitPromoteSerial(o *promoteOpts, tr *transfer.Tracker, plans []*promotePlan) {
	pids := make([]int64, len(plans))
	files, bytes := 0, int64(0)
	for i, p := range plans {
		pids[i] = addPendingPromote(o, p)
		files += p.files
		bytes += p.bytes
	}
	label := safeBaseName(plans[0].src, plans[0].d.Name)
	if len(plans) > 1 {
		label = fmt.Sprintf("%d items", len(plans))
	}
	tr.SubmitFor(o.userID, label, "promote", files, bytes, func(job *transfer.Job) {
		var firstErr error
		for i, p := range plans {
			if err := runPromotePlan(o, p, job); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				log.Printf("promote: #%d %q failed: %v", p.d.ID, httpshared.SanitizeForLog(p.d.Name), err)
				continue
			}
			_ = o.pending.Remove(pids[i])
		}
		if firstErr != nil {
			job.Fail(firstErr)
			return
		}
		job.Done()
	})
}

// addPendingPromote persists a promotion's intent (resume on boot) and returns
// the id used to remove it on completion.
func addPendingPromote(o *promoteOpts, p *promotePlan) int64 {
	payload, _ := json.Marshal(promotePayload{DownloadID: p.d.ID, UserID: o.userID, KeepSeeding: o.keepSeeding})
	pid, _ := o.pending.Add(transfer.Pending{Kind: "promote", Src: p.src, Dst: p.dst, Payload: string(payload)})
	return pid
}

func promoteTargetDir(o *promoteOpts) (string, error) {
	subdir, err := httpshared.SanitizeSubdir(o.targetSubdir)
	if err != nil {
		return "", err
	}
	if subdir == "" {
		return o.sharedDir, nil
	}
	return filepath.Join(o.sharedDir, subdir), nil
}

func safeBaseName(src, fallbackName string) string {
	baseName := filepath.Base(src)
	if baseName == "" || baseName == "." || baseName == "/" {
		return fallbackName
	}
	return baseName
}

func promoteDestPath(o *promoteOpts, baseName string, targetDir *string) string {
	if o.renameIA && o.aiClient != nil {
		preview, err := renamer.GeneratePreview(context.Background(), o.aiClient, o.tmdbClient, baseName)
		if err == nil && preview != nil {
			targetRel := renamer.ResolveTargetConflict(o.sharedDir, preview.TargetPath)
			dst := filepath.Join(o.sharedDir, targetRel)
			*targetDir = filepath.Dir(dst)
			return dst
		}
	}
	return filepath.Join(*targetDir, baseName)
}

func ensureTargetDir(targetDir string) error {
	// #nosec G301 -- media/cache dir; 0755 intentional so the media server can read it
	return os.MkdirAll(targetDir, 0755)
}

// applySeedingAfterPromote re-points or stops the torrent after its file was
// moved to the promote destination. keepSeeding=false → Drop (stop seeding).
// keepSeeding=true → Drop + re-add so anacrolix picks up the relocatedStorage at
// the NEW path and keeps seeding IMMEDIATELY. Without the re-add the live torrent
// kept pointing at the old (now-moved) file and silently stopped serving until
// the next boot auto-seed — "keepSeeding" didn't actually keep it sending.
// Mirrors the downloads worker's reseedAfterCompletion.
func applySeedingAfterPromote(o *promoteOpts, d *downloads.Download) {
	if d.InfoHash == "" {
		return
	}
	var h metainfo.Hash
	if err := h.FromHexString(d.InfoHash); err != nil {
		return
	}
	if !o.keepSeeding {
		// User promoted WITHOUT "keep seeding" → stop for good, clear the
		// persisted auto-seed and mark the completed row as seed-stopped so the
		// next boot does not reactivate the torrent. Best-effort: the promote
		// outcome does not depend on the seed teardown.
		_ = o.s.DropSeed(h)
		if d.Status == downloads.StatusCompleted {
			_ = o.store.StopSeed(d.UserID, d.ID)
		}
		return
	}
	o.s.Drop(h)
	// file_path was just updated to the new destination, so EnsureActive's
	// relocatedStorage resolves the moved file and seeds it in place (no re-download).
	// SeedSource prefers a bare info_hash magnet → the cached metainfo resolves it
	// without re-fetching the (often dead) origin .torrent URL.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := o.s.EnsureActive(ctx, d.SeedSource()); err != nil {
		log.Printf("promote: reseed #%d %q from new location failed: %v", d.ID, httpshared.SanitizeForLog(d.Name), err)
	}
}

// DownloadsPromoteDests handles GET /api/promote/destinations — returns the
// list of available promote destinations (name + path). The first is always
// "Biblioteca" (sharedDir), followed by the ones configured in promote_dirs.
func DownloadsPromoteDests(sharedDir string, dests []httpshared.PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, BuildPromoteDests(sharedDir, dests))
	}
}

// DownloadsStopSeed handles POST /api/downloads/:id/stop-seed — "Stop" in the UI.
// Stops seeding AND removes the row from the downloads list (the files stay on
// disk): the user expects the torrent to vanish from the downloads area, not to
// merely migrate from "Seeding" to "On disk". DropSeed clears the persisted
// auto-seed so the next boot does not resurrect the torrent.
func DownloadsStopSeed(store *downloads.Store, s *streamer.Streamer, worker DownloadRemover) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		d, err := store.Get(userID, id)
		if err != nil || d == nil {
			httpshared.RespondErrorMessage(c, http.StatusNotFound, errDownloadNotFound)
			return
		}
		if d.InfoHash != "" {
			var h metainfo.Hash
			if err := h.FromHexString(d.InfoHash); err == nil {
				// "Stop seeding" is explicit → DropSeed also clears the persisted
				// auto-seed so the next boot does not reactivate the torrent.
				// Best-effort: the row is deleted right after regardless.
				_ = s.DropSeed(h)
			}
		}
		// Any status (completed, paused, …): the row leaves the list. The old
		// status='completed' guard made the pause→stop flow have no effect at all
		// on the listing.
		row, err := store.DeleteScoped(userID, id, isAdmin)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		if row != nil {
			log.Printf("downloads: stopSeed user=%d id=%d infoHash=%s name=%q admin=%v", userID, row.ID, httpshared.SanitizeForLog(row.InfoHash), httpshared.SanitizeForLog(row.Name), isAdmin)
		}
		notifyRemoved(worker, row)
		c.Status(http.StatusNoContent)
	}
}
