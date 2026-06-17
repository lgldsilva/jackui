package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/renamer"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/tmdb"
	"github.com/lgldsilva/jackui/internal/transfer"
)

const (
	errSharedDirNotConfig = "JACKUI_SHARED_DIR não configurado"
	errDownloadNotFound   = "download não encontrado"
)

// PromoteDest represents a named promote destination (shared dir or extra).
type PromoteDest struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// promoteReq é o body de POST /api/downloads/:id/promote e do batch handler.
// targetSubdir é relativo a sharedDir; valida-se contra path traversal.
type promoteReq struct {
	KeepSeeding  bool   `json:"keepSeeding"`
	TargetSubdir string `json:"targetSubdir"`
	TargetBase   string `json:"targetBase"` // empty = sharedDir (default)
	RenameIA     bool   `json:"renameIA"`
	// Apenas para batch:
	IDs []int `json:"ids"`
}

// BuildPromoteDests returns the full list of promote destinations: sharedDir
// is always first ("Biblioteca"), followed by any configured extras.
func BuildPromoteDests(sharedDir string, extra []PromoteDest) []PromoteDest {
	dests := []PromoteDest{}
	if sharedDir != "" {
		dests = append(dests, PromoteDest{Name: "Biblioteca", Path: sharedDir})
	}
	dests = append(dests, extra...)
	return dests
}

// resolveTargetBase resolves a targetBase string against the list of
// destinations. If targetBase is empty, returns sharedDir (default). Returns
// error if targetBase doesn't match any destination path.
func resolveTargetBase(targetBase, sharedDir string, dests []PromoteDest) (string, error) {
	if targetBase == "" {
		return sharedDir, nil
	}
	for _, d := range dests {
		if d.Path == targetBase {
			return d.Path, nil
		}
	}
	return "", errors.New("destino inválido: " + targetBase)
}

// sanitizeSubdir valida o subdir digitado pelo usuário pra não escapar do
// sharedDir via "..", caminhos absolutos. Retorna o caminho limpo (Clean) ou
// erro descritivo.
func sanitizeSubdir(subdir string) (string, error) {
	if subdir == "" {
		return "", nil
	}
	if filepath.IsAbs(subdir) {
		return "", errors.New("subdir não pode ser absoluto")
	}
	clean := filepath.Clean(subdir)
	for _, seg := range strings.Split(clean, string(filepath.Separator)) {
		if seg == ".." {
			return "", errors.New("subdir não pode conter '..'")
		}
	}
	if clean == "." {
		return "", nil
	}
	return clean, nil
}

// DownloadsPromote handles POST /api/downloads/:id/promote — move um download
// concluído pra sharedDir (opcionalmente em targetSubdir). Body:
//
//	{ "keepSeeding": bool, "targetSubdir": "movies/2026", "targetBase": "/mnt/gdrive/media" }
//
// targetSubdir vazio = raiz do destino. targetBase vazio = sharedDir (default).
// Subpastas inexistentes são criadas (os.MkdirAll). Validação anti-traversal.
func DownloadsPromote(store *downloads.Store, s *streamer.Streamer, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []PromoteDest, tr *transfer.Tracker) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": errSharedDirNotConfig})
			return
		}
		var req promoteReq
		_ = c.ShouldBindJSON(&req)

		base, err := resolveTargetBase(req.TargetBase, sharedDir, dests)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		userID, _, _ := auth.UserIDFromCtx(c)
		updated, err := promoteOne(&promoteOpts{store: store, s: s, aiClient: aiClient, tmdbClient: tmdbClient, sharedDir: base, userID: userID, id: id, targetSubdir: req.TargetSubdir, keepSeeding: req.KeepSeeding, renameIA: req.RenameIA, tracker: tr})
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, updated)
	}
}

// DownloadsPromoteBatch handles POST /api/downloads/promote — promove uma
// lista de downloads pro MESMO targetSubdir. Body:
//
//	{ "ids": [1,2,3], "targetSubdir": "movies", "keepSeeding": false, "targetBase": "/mnt/gdrive/media" }
//
// Resposta: { "promoted": [<DownloadEntry>...], "failed": [{id, error}...] }
// Falhas individuais não abortam o batch — cada item é tentado.
func DownloadsPromoteBatch(store *downloads.Store, s *streamer.Streamer, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []PromoteDest, tr *transfer.Tracker) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": errSharedDirNotConfig})
			return
		}
		req, base, ok := validateBatchReq(c, sharedDir, dests)
		if !ok {
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		promoted, failed := promoteBatchItems(&promoteOpts{store: store, s: s, aiClient: aiClient, tmdbClient: tmdbClient, sharedDir: base, userID: userID, targetSubdir: req.TargetSubdir, keepSeeding: req.KeepSeeding, renameIA: req.RenameIA, tracker: tr}, req)
		c.JSON(http.StatusOK, gin.H{"promoted": promoted, "failed": failed})
	}
}

func validateBatchReq(c *gin.Context, sharedDir string, dests []PromoteDest) (*promoteReq, string, bool) {
	var req promoteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, "", false
	}
	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids vazio"})
		return nil, "", false
	}
	base, err := resolveTargetBase(req.TargetBase, sharedDir, dests)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, "", false
	}
	return &req, base, true
}

func promoteBatchItems(o *promoteOpts, req *promoteReq) ([]downloads.Download, []gin.H) {
	promoted := []downloads.Download{}
	failed := []gin.H{}
	for _, id := range req.IDs {
		o.id = id
		d, err := promoteOne(o)
		if err != nil {
			failed = append(failed, gin.H{"id": id, "error": err.Error()})
			continue
		}
		if d != nil {
			promoted = append(promoted, *d)
		}
	}
	return promoted, failed
}

func DownloadsPromotePreview(store *downloads.Store, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": errSharedDirNotConfig})
			return
		}
		var req promoteReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if len(req.IDs) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ids vazio"})
			return
		}
		base, err := resolveTargetBase(req.TargetBase, sharedDir, dests)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		return gin.H{"id": id, "error": errDownloadNotFound}
	}
	if dl.FilePath == "" {
		return gin.H{"id": id, "error": "file_path vazio"}
	}
	rawName := filepath.Base(dl.FilePath)
	if rawName == "" || rawName == "." || rawName == "/" {
		rawName = dl.Name
	}
	preview, err := renamer.GeneratePreview(d.ctx, d.aiClient, d.tmdbClient, rawName)
	if err != nil {
		return gin.H{"id": id, "error": err.Error()}
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
// — lista subpastas em {base}/path pra alimentar o navegador da UI. base
// vazio = sharedDir. Não expõe arquivos (só dirs).
func DownloadsPromoteBrowse(sharedDir string, dests []PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": errSharedDirNotConfig})
			return
		}
		root, err := resolveTargetBase(c.Query("base"), sharedDir, dests)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		sub, err := sanitizeSubdir(c.Query("path"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		dir := joinIfSub(root, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"dirs": []string{}, "path": sub})
			return
		}
		c.JSON(http.StatusOK, gin.H{"dirs": listDirs(entries), "path": sub})
	}
}

func joinIfSub(root, sub string) string {
	if sub == "" {
		return root
	}
	return filepath.Join(root, sub)
}

func listDirs(entries []os.DirEntry) []string {
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return dirs
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
}

func promoteOne(o *promoteOpts) (*downloads.Download, error) {
	d, err := o.store.Get(o.userID, o.id)
	if err != nil || d == nil {
		return nil, errors.New(errDownloadNotFound)
	}
	if d.Status != downloads.StatusCompleted {
		return nil, errors.New("só downloads concluídos podem ser promovidos")
	}
	if d.FilePath == "" {
		return nil, errors.New("file_path vazio — nada pra promover")
	}
	targetDir, err := promoteTargetDir(o)
	if err != nil {
		return nil, err
	}
	src := d.FilePath
	baseName := safeBaseName(src, d.Name)
	dst := promoteDestPath(o, baseName, &targetDir)
	if src == dst {
		return d, nil
	}
	if _, statErr := os.Stat(src); statErr != nil {
		return nil, errors.New("arquivo de origem não existe: " + statErr.Error())
	}
	if err := ensureTargetDir(targetDir); err != nil {
		return nil, err
	}
	files, bytes := countTree(src)
	job := o.tracker.Start(baseName, "promote", files, bytes)
	if err := moveWithFallbackJob(src, dst, job, files, bytes); err != nil {
		job.Fail(err)
		return nil, errors.New("mover arquivo: " + err.Error())
	}
	job.Done()
	_ = o.store.SetFilePath(o.userID, o.id, dst)
	stopSeedingIfNeeded(o, d.InfoHash)
	return o.store.Get(o.userID, o.id)
}

func promoteTargetDir(o *promoteOpts) (string, error) {
	subdir, err := sanitizeSubdir(o.targetSubdir)
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
	return os.MkdirAll(targetDir, 0755)
}

func moveWithFallback(src, dst string) error {
	return moveWithFallbackJob(src, dst, nil, 0, 0)
}

// moveWithFallbackJob is moveWithFallback with transfer-progress reporting:
// files/bytes are reported in one shot for the instant same-fs rename; the
// cross-device copy fallback meters itself.
func moveWithFallbackJob(src, dst string, job *transfer.Job, files int, bytes int64) error {
	if err := os.Rename(src, dst); err != nil {
		return promoteCopyDeleteJob(src, dst, job)
	}
	reportInstantMove(job, files, bytes)
	return nil
}

func stopSeedingIfNeeded(o *promoteOpts, infoHash string) {
	if o.keepSeeding {
		return
	}
	var h metainfo.Hash
	if err := h.FromHexString(infoHash); err == nil {
		o.s.Drop(h)
	}
}

// DownloadsPromoteDests handles GET /api/promote/destinations — retorna a
// lista de destinos de promoção disponíveis (nome + path). O primeiro é
// sempre "Biblioteca" (sharedDir), seguido pelos configurados em promote_dirs.
func DownloadsPromoteDests(sharedDir string, dests []PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, BuildPromoteDests(sharedDir, dests))
	}
}

// DownloadsStopSeed handles POST /api/downloads/:id/stop-seed — derruba o
// torrent do anacrolix sem mover o arquivo.
func DownloadsStopSeed(store *downloads.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		d, err := store.Get(userID, id)
		if err != nil || d == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": errDownloadNotFound})
			return
		}
		if d.InfoHash != "" {
			var h metainfo.Hash
			if err := h.FromHexString(d.InfoHash); err == nil {
				s.Drop(h)
			}
		}
		c.Status(http.StatusNoContent)
	}
}

// promoteCopyDelete handles cross-filesystem moves (Rename fails when src and
// dst live on different mount points). Copies content then removes the source.
// Best-effort cleanup: removes the partial dst on error.
func promoteCopyDelete(src, dst string) error { return promoteCopyDeleteJob(src, dst, nil) }

func promoteCopyDeleteJob(src, dst string, job *transfer.Job) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, transfer.ProgressReader(in, job.AddBytesFunc())); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	job.FileDone()
	return os.Remove(src)
}
