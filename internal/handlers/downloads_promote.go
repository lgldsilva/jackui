package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

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
func DownloadsPromote(store *downloads.Store, s *streamer.Streamer, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []PromoteDest, tr *transfer.Tracker, pending *transfer.Store) gin.HandlerFunc {
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
		o := &promoteOpts{store: store, s: s, aiClient: aiClient, tmdbClient: tmdbClient, sharedDir: base, userID: userID, id: id, targetSubdir: req.TargetSubdir, keepSeeding: req.KeepSeeding, renameIA: req.RenameIA, tracker: tr, pending: pending}
		plan, err := promotePreparePlan(o)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if plan != nil {
			// Cópia roda em background (pool de transferências) → responde na hora.
			submitPromotePlans(o, tr, []*promotePlan{plan})
		}
		// Retorno otimista: file_path ainda aponta pro original até a cópia terminar
		// (a lista reflete o destino quando o job do dock conclui).
		updated, _ := store.Get(userID, id)
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
func DownloadsPromoteBatch(store *downloads.Store, s *streamer.Streamer, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []PromoteDest, tr *transfer.Tracker, pending *transfer.Store) gin.HandlerFunc {
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
		promoted, failed := promoteBatchItems(&promoteOpts{store: store, s: s, aiClient: aiClient, tmdbClient: tmdbClient, sharedDir: base, userID: userID, targetSubdir: req.TargetSubdir, keepSeeding: req.KeepSeeding, renameIA: req.RenameIA, tracker: tr, pending: pending}, req, tr)
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

// promoteBatchItems valida cada item SINCRONAMENTE (erros voltam na resposta) e
// submete os válidos pra cópia em background. `promoted` é otimista: lista os
// aceitos (a cópia roda no painel de Transferências); `failed` traz só os que
// falharam na validação. Isso mantém o shape da resposta e evita o 504 numa
// cópia grande síncrona.
func promoteBatchItems(o *promoteOpts, req *promoteReq, tr *transfer.Tracker) ([]downloads.Download, []gin.H) {
	promoted := []downloads.Download{}
	failed := []gin.H{}
	var plans []*promotePlan
	for _, id := range req.IDs {
		o.id = id
		plan, err := promotePreparePlan(o)
		if err != nil {
			failed = append(failed, gin.H{"id": id, "error": err.Error()})
			continue
		}
		if plan == nil { // já no destino — sucesso imediato, sem cópia
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
	// Non-nil slice so a folder with no subdirs serializes as JSON [] (not null):
	// the UI does `dirs.length` on the result, and a nil slice → null → crash.
	dirs := []string{}
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
	pending      *transfer.Store   // persists the copy intent so a restart can resume it (nil-safe)
}

// promotePayload is the kind-specific JSON stored with a pending promote, so the
// boot reconciler can finish the copy AND re-point the download row + re-seed.
type promotePayload struct {
	DownloadID  int  `json:"downloadID"`
	UserID      int  `json:"userID"`
	KeepSeeding bool `json:"keepSeeding"`
}

// promotePlan é um promote validado pronto pra copiar. A validação é síncrona
// (rápida); a cópia (movePathJob) roda depois em background — ver runPromotePlan.
type promotePlan struct {
	d       *downloads.Download
	src     string
	dst     string
	srcInfo os.FileInfo
	files   int
	bytes   int64
}

// promotePreparePlan valida e resolve os caminhos SINCRONAMENTE (sem copiar).
// Retorna (plan, nil) quando há o que mover; (nil, nil) quando o arquivo já está
// no destino (no-op); (nil, err) em falha de validação — reportável na resposta
// imediata. Separar a validação da cópia deixa o handler responder na hora e
// jogar a cópia (lenta em arquivo grande) pro pool de transferências, evitando
// o 504 do proxy reverso numa cópia síncrona longa.
func promotePreparePlan(o *promoteOpts) (*promotePlan, error) {
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
		return nil, nil // já no lugar
	}
	srcInfo, statErr := os.Stat(src)
	if statErr != nil {
		return nil, errors.New("arquivo de origem não existe: " + statErr.Error())
	}
	if err := ensureTargetDir(targetDir); err != nil {
		return nil, err
	}
	files, bytes := countTree(src)
	return &promotePlan{d: d, src: src, dst: dst, srcInfo: srcInfo, files: files, bytes: bytes}, nil
}

// runPromotePlan executa a cópia + pós-processamento de um plano. Roda DENTRO do
// job de transferência (background); job pode ser nil (reporte vira no-op).
// movePathJob trata arquivo E diretório (whole-torrent é uma pasta).
func runPromotePlan(o *promoteOpts, p *promotePlan, job *transfer.Job) error {
	if err := movePathJob(p.src, p.dst, p.srcInfo, job, p.files, p.bytes); err != nil {
		return errors.New("mover arquivo: " + err.Error())
	}
	_ = o.store.SetFilePath(o.userID, p.d.ID, p.dst)
	applySeedingAfterPromote(o, p.d)
	return nil
}

// submitPromotePlans copia os planos validados em background, num único job
// agregado do pool de transferências — para a request retornar na hora (sem
// estourar o timeout do proxy numa cópia grande). O painel de Transferências
// mostra o progresso; a lista de downloads reflete o novo file_path ao concluir.
func submitPromotePlans(o *promoteOpts, tr *transfer.Tracker, plans []*promotePlan) {
	// Um job POR item (não um job agregado): o pool de transferências roda vários
	// em paralelo (até defaultMaxConcurrent) e enfileira o resto, então o usuário
	// vê o progresso por arquivo no painel e várias cópias andam ao mesmo tempo —
	// em vez de uma fila sequencial dentro de um job único.
	for _, p := range plans {
		p := p // captura por iteração
		label := safeBaseName(p.src, p.d.Name)
		// Persiste a intenção ANTES de copiar: se o processo cair/reiniciar no
		// meio, o boot re-submete (resume-aware: pula o que já foi copiado).
		// Removida ao concluir com sucesso.
		payload, _ := json.Marshal(promotePayload{DownloadID: p.d.ID, UserID: o.userID, KeepSeeding: o.keepSeeding})
		pid, _ := o.pending.Add(transfer.Pending{Kind: "promote", Src: p.src, Dst: p.dst, Payload: string(payload)})
		tr.Submit(label, "promote", p.files, p.bytes, func(job *transfer.Job) {
			if err := runPromotePlan(o, p, job); err != nil {
				job.Fail(err)
				log.Printf("promote: #%d %q falhou: %v", p.d.ID, p.d.Name, err)
				return
			}
			_ = o.pending.Remove(pid)
			job.Done()
		})
	}
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
	o.s.Drop(h)
	if !o.keepSeeding {
		return
	}
	// file_path was just updated to the new destination, so EnsureActive's
	// relocatedStorage resolves the moved file and seeds it in place (no re-download).
	// SeedSource prefers a bare info_hash magnet → the cached metainfo resolves it
	// without re-fetching the (often dead) origin .torrent URL.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := o.s.EnsureActive(ctx, d.SeedSource()); err != nil {
		log.Printf("promote: reseed #%d %q from new location failed: %v", d.ID, d.Name, err)
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

