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

	"github.com/luizg/jackui/internal/ai"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/renamer"
	"github.com/luizg/jackui/internal/streamer"
	"github.com/luizg/jackui/internal/tmdb"
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
func DownloadsPromote(store *downloads.Store, s *streamer.Streamer, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": "JACKUI_SHARED_DIR não configurado"})
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
		updated, err := promoteOne(store, s, aiClient, tmdbClient, base, userID, id, req.TargetSubdir, req.KeepSeeding, req.RenameIA)
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
func DownloadsPromoteBatch(store *downloads.Store, s *streamer.Streamer, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": "JACKUI_SHARED_DIR não configurado"})
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
		promoted := []downloads.Download{}
		failed := []gin.H{}
		for _, id := range req.IDs {
			d, err := promoteOne(store, s, aiClient, tmdbClient, base, userID, id, req.TargetSubdir, req.KeepSeeding, req.RenameIA)
			if err != nil {
				failed = append(failed, gin.H{"id": id, "error": err.Error()})
				continue
			}
			if d != nil {
				promoted = append(promoted, *d)
			}
		}
		c.JSON(http.StatusOK, gin.H{"promoted": promoted, "failed": failed})
	}
}

// DownloadsPromotePreview handles POST /api/downloads/promote/preview
func DownloadsPromotePreview(store *downloads.Store, aiClient *ai.Client, tmdbClient *tmdb.Client, sharedDir string, dests []PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": "JACKUI_SHARED_DIR não configurado"})
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
		previews := []gin.H{}
		for _, id := range req.IDs {
			d, err := store.Get(userID, id)
			if err != nil || d == nil {
				previews = append(previews, gin.H{"id": id, "error": "download não encontrado"})
				continue
			}
			if d.FilePath == "" {
				previews = append(previews, gin.H{"id": id, "error": "file_path vazio"})
				continue
			}
			rawName := filepath.Base(d.FilePath)
			if rawName == "" || rawName == "." || rawName == "/" {
				rawName = d.Name
			}
			preview, err := renamer.GeneratePreview(c.Request.Context(), aiClient, tmdbClient, rawName)
			if err != nil {
				previews = append(previews, gin.H{"id": id, "error": err.Error()})
				continue
			}
			nonConflicting := renamer.ResolveTargetConflict(base, preview.TargetPath)
			previews = append(previews, gin.H{
				"id":           id,
				"originalName": rawName,
				"cleanName":    preview.CleanName,
				"targetPath":   nonConflicting,
				"kind":         preview.Kind,
				"year":         preview.Year,
				"season":       preview.Season,
				"episode":      preview.Episode,
				"episodeName":  preview.EpisodeName,
			})
		}
		c.JSON(http.StatusOK, gin.H{"previews": previews})
	}
}

// DownloadsPromoteBrowse handles GET /api/downloads/promote/browse?path=movies&base=/mnt/gdrive/media
// — lista subpastas em {base}/path pra alimentar o navegador da UI. base
// vazio = sharedDir. Não expõe arquivos (só dirs).
func DownloadsPromoteBrowse(sharedDir string, dests []PromoteDest) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedDir == "" {
			c.JSON(http.StatusConflict, gin.H{"error": "JACKUI_SHARED_DIR não configurado"})
			return
		}
		base := c.Query("base")
		root, err := resolveTargetBase(base, sharedDir, dests)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		sub, err := sanitizeSubdir(c.Query("path"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		dir := root
		if sub != "" {
			dir = filepath.Join(root, sub)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"dirs": []string{}, "path": sub})
			return
		}
		dirs := []string{}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				dirs = append(dirs, e.Name())
			}
		}
		sort.Strings(dirs)
		c.JSON(http.StatusOK, gin.H{"dirs": dirs, "path": sub})
	}
}

// promoteOne implementa a lógica compartilhada entre single + batch. Validação
// + move + (opcional) drop do torrent.
func promoteOne(
	store *downloads.Store,
	s *streamer.Streamer,
	aiClient *ai.Client,
	tmdbClient *tmdb.Client,
	sharedDir string,
	userID, id int,
	targetSubdir string,
	keepSeeding bool,
	renameIA bool,
) (*downloads.Download, error) {
	d, err := store.Get(userID, id)
	if err != nil || d == nil {
		return nil, errors.New("download não encontrado")
	}
	if d.Status != downloads.StatusCompleted {
		return nil, errors.New("só downloads concluídos podem ser promovidos")
	}
	if d.FilePath == "" {
		return nil, errors.New("file_path vazio — nada pra promover")
	}
	subdir, err := sanitizeSubdir(targetSubdir)
	if err != nil {
		return nil, err
	}
	targetDir := sharedDir
	if subdir != "" {
		targetDir = filepath.Join(sharedDir, subdir)
	}

	src := d.FilePath
	baseName := filepath.Base(src)
	if baseName == "" || baseName == "." || baseName == "/" {
		baseName = d.Name
	}

	var dst string
	if renameIA && aiClient != nil {
		preview, err := renamer.GeneratePreview(context.Background(), aiClient, tmdbClient, baseName)
		if err == nil && preview != nil {
			targetRel := renamer.ResolveTargetConflict(sharedDir, preview.TargetPath)
			dst = filepath.Join(sharedDir, targetRel)
			targetDir = filepath.Dir(dst)
		}
	}

	if dst == "" {
		dst = filepath.Join(targetDir, baseName)
	}

	if src == dst {
		return d, nil // idempotente
	}
	if _, statErr := os.Stat(src); statErr != nil {
		return nil, errors.New("arquivo de origem não existe: " + statErr.Error())
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, errors.New("criar destino: " + err.Error())
	}
	if err := os.Rename(src, dst); err != nil {
		if err := promoteCopyDelete(src, dst); err != nil {
			return nil, errors.New("mover arquivo: " + err.Error())
		}
	}
	_ = store.SetFilePath(userID, id, dst)
	if !keepSeeding {
		var h metainfo.Hash
		if err := h.FromHexString(d.InfoHash); err == nil {
			s.Drop(h)
		}
	}
	return store.Get(userID, id)
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		d, err := store.Get(userID, id)
		if err != nil || d == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "download não encontrado"})
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
func promoteCopyDelete(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return os.Remove(src)
}
