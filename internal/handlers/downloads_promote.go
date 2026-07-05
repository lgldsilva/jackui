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
	errDownloadNotFound = "download não encontrado"
)

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

// DownloadsPromote handles POST /api/downloads/:id/promote — move um download
// concluído pra sharedDir (opcionalmente em targetSubdir). Body:
//
//	{ "keepSeeding": bool, "targetSubdir": "movies/2026", "targetBase": "/mnt/gdrive/media" }
//
// targetSubdir vazio = raiz do destino. targetBase vazio = sharedDir (default).
// Subpastas inexistentes são criadas (os.MkdirAll). Validação anti-traversal.
func DownloadsPromote(d PromoteDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		if d.SharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": httpshared.ErrSharedDirNotConfig})
			return
		}
		var req promoteReq
		_ = c.ShouldBindJSON(&req)

		base, err := httpshared.ResolveTargetBase(req.TargetBase, d.SharedDir, d.Dests)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		userID, _, _ := auth.UserIDFromCtx(c)
		o := &promoteOpts{store: d.Store, s: d.Streamer, aiClient: d.AIClient, tmdbClient: d.TMDBClient, sharedDir: base, userID: userID, id: id, targetSubdir: req.TargetSubdir, keepSeeding: req.KeepSeeding, renameIA: req.RenameIA, tracker: d.Tracker, pending: d.Pending, concMode: transferMode(d.Cfg)}
		plan, err := promotePreparePlan(o)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if plan != nil {
			// Cópia roda em background (pool de transferências) → responde na hora.
			submitPromotePlans(o, d.Tracker, []*promotePlan{plan})
		}
		// Retorno otimista: file_path ainda aponta pro original até a cópia terminar
		// (a lista reflete o destino quando o job do dock conclui).
		updated, _ := d.Store.Get(userID, id)
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
func DownloadsPromoteBatch(d PromoteDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.SharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": httpshared.ErrSharedDirNotConfig})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, "", false
	}
	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids vazio"})
		return nil, "", false
	}
	base, err := httpshared.ResolveTargetBase(req.TargetBase, sharedDir, dests)
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

func DownloadsPromotePreview(store *downloads.Store, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []httpshared.PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": httpshared.ErrSharedDirNotConfig})
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
		base, err := httpshared.ResolveTargetBase(req.TargetBase, sharedDir, dests)
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
func DownloadsPromoteBrowse(sharedDir string, dests []httpshared.PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": httpshared.ErrSharedDirNotConfig})
			return
		}
		root, err := httpshared.ResolveTargetBase(c.Query("base"), sharedDir, dests)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		sub, err := httpshared.SanitizeSubdir(c.Query("path"))
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
	concMode     string            // "" / "auto" / "serial" / "parallel" — ver TransferConcurrencyMode
}

// Modos de concorrência de transferência (config TransferConcurrencyMode).
const (
	transferModeAuto     = "auto"
	transferModeSerial   = "serial"
	transferModeParallel = "parallel"
)

// transferMode lê o modo de concorrência da config AO VIVO (nil-safe → "").
func transferMode(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Stream.TransferConcurrencyMode
}

// shouldSerialize decide, pelo modo + disco destino, se as cópias rodam uma de
// cada vez. "auto" (default) detecta HDD; "serial"/"parallel" forçam.
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

// promotePlan é um promote validado pronto pra copiar. A validação é síncrona
// (rápida); a cópia (lh.MovePathJob) roda depois em background — ver runPromotePlan.
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
	files, bytes := lh.CountTree(src)
	return &promotePlan{d: d, src: src, dst: dst, srcInfo: srcInfo, files: files, bytes: bytes}, nil
}

// runPromotePlan executa a cópia + pós-processamento de um plano. Roda DENTRO do
// job de transferência (background); job pode ser nil (reporte vira no-op).
// lh.MovePathJob trata arquivo E diretório (whole-torrent é uma pasta).
func runPromotePlan(o *promoteOpts, p *promotePlan, job *transfer.Job) error {
	if err := lh.MovePathJob(p.src, p.dst, p.srcInfo, job, p.files, p.bytes); err != nil {
		return errors.New("mover arquivo: " + err.Error())
	}
	_ = o.store.SetFilePath(o.userID, p.d.ID, p.dst)
	applySeedingAfterPromote(o, p.d)
	return nil
}

// submitPromotePlans copia os planos validados em background. A estratégia
// depende do disco DESTINO:
//   - HDD (rotacional): UM job sequencial. Cópias paralelas no mesmo HDD fazem
//     a cabeça do disco buscar entre elas (seek thrashing) e o throughput
//     agregado despenca vs uma cópia de cada vez.
//   - SSD/NVMe: UM job por item → o pool roda vários em paralelo, com progresso
//     por arquivo, sem penalidade de seek.
//
// Em ambos os casos a intenção é persistida ANTES de copiar (resume no boot) e
// removida ao concluir, e a request retorna na hora (sem 504).
func submitPromotePlans(o *promoteOpts, tr *transfer.Tracker, plans []*promotePlan) {
	if len(plans) == 0 {
		return
	}
	if shouldSerialize(o.concMode, plans[0].dst) {
		submitPromoteSerial(o, tr, plans)
		return
	}
	for _, p := range plans {
		p := p // captura por iteração
		pid := addPendingPromote(o, p)
		tr.Submit(safeBaseName(p.src, p.d.Name), "promote", p.files, p.bytes, func(job *transfer.Job) {
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

// submitPromoteSerial copia todos os planos num único job, um de cada vez —
// usado quando o destino é um HDD (evita seek thrashing de cópias paralelas).
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
		label = fmt.Sprintf("%d itens", len(plans))
	}
	tr.Submit(label, "promote", files, bytes, func(job *transfer.Job) {
		var firstErr error
		for i, p := range plans {
			if err := runPromotePlan(o, p, job); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				log.Printf("promote: #%d %q falhou: %v", p.d.ID, p.d.Name, err)
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

// addPendingPromote persiste a intenção de uma promoção (resume no boot) e
// devolve o id pra removê-la ao concluir.
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
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
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
		// Usuário promoveu SEM "continuar seedando" → para de vez e limpa o
		// auto-seed persistido (senão voltaria a seedar no próximo boot).
		o.s.DropSeed(h)
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
		log.Printf("promote: reseed #%d %q from new location failed: %v", d.ID, d.Name, err)
	}
}

// DownloadsPromoteDests handles GET /api/promote/destinations — retorna a
// lista de destinos de promoção disponíveis (nome + path). O primeiro é
// sempre "Biblioteca" (sharedDir), seguido pelos configurados em promote_dirs.
func DownloadsPromoteDests(sharedDir string, dests []httpshared.PromoteDest) gin.HandlerFunc {
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
				// "Parar de seedar" é explícito → DropSeed limpa também o auto-seed
				// persistido, senão voltaria a seedar no próximo boot.
				s.DropSeed(h)
			}
		}
		c.Status(http.StatusNoContent)
	}
}
