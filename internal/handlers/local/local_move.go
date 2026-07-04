package local

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// Move / rename / primitivas de cópia-e-remoção — extraído de local.go.
// LocalMoveEntry handles POST /api/local/move — moves a file or directory
// from one mount to another (or within the same mount). Admin only.
// Body: { srcMount, srcPath, dstMount, dstPath (target directory) }
func LocalMoveEntry(b *lb.Browser, dls *downloads.Store, s *streamer.Streamer, tr *transfer.Tracker) gin.HandlerFunc {
	return func(c *gin.Context) {
		localMoveHandler(c, b, dls, s, tr)
	}
}

func localMoveHandler(c *gin.Context, b *lb.Browser, dls *downloads.Store, s *streamer.Streamer, tr *transfer.Tracker) {
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
	if !CheckMountAccess(b, c, req.SrcMount) || !CheckMountAccess(b, c, req.DstMount) {
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

	// The move is submitted to the bounded transfer pool (waits FIFO for a slot,
	// status 'queued' in the dock) and runs off-request, so a large cross-fs copy
	// (→ GDrive/other disk) neither blocks the request past the reverse-proxy
	// timeout nor thrashes the disk against other transfers. The UI follows
	// progress via the dock and refreshes the listing when the job finishes.
	files, total := CountTree(srcAbs)
	label := filepath.Base(req.SrcPath)
	moved := filepath.Join(req.DstMount, req.DstPath, filepath.Base(req.SrcPath))
	job := tr.Submit(label, "local-move", files, total, func(job *transfer.Job) {
		if err := MovePathJob(srcAbs, dstAbs, srcStat, job, files, total); err != nil {
			job.Fail(err)
			return
		}
		relinkMovedTorrents(dls, s, srcAbs, dstAbs)
		job.Done()
	})
	c.JSON(http.StatusAccepted, gin.H{"moved": moved, "jobId": job.ID(), "async": true})
}

func isAdminMove(c *gin.Context) bool {
	claims, _ := auth.ClaimsFromCtx(c)
	if claims == nil || claims.Role != auth.RoleAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "apenas admins podem mover entre mounts"})
		return false
	}
	return true
}

func resolveSource(b *lb.Browser, c *gin.Context, req *moveEntryReq) (string, os.FileInfo, error) {
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

func resolveDest(b *lb.Browser, c *gin.Context, req *moveEntryReq, srcAbs string) (string, error) {
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

// CountTree returns the number of regular files under path and their total byte
// size — file → (1, size); dir → recursive totals. Used to seed a transfer.Job
// (X/Y files, bytes) before a move starts. Best-effort: unreadable entries are
// skipped (the move itself surfaces real errors).
func CountTree(path string) (files int, bytes int64) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	if !st.IsDir() {
		return 1, st.Size()
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		f, b := CountTree(filepath.Join(path, e.Name()))
		files += f
		bytes += b
	}
	return files, bytes
}

// reportInstantMove fast-forwards a job by an item's precomputed size — for a
// same-filesystem os.Rename, which is instantaneous (no copy loop to meter).
func reportInstantMove(job *transfer.Job, files int, bytes int64) {
	job.AddBytes(bytes)
	for i := 0; i < files; i++ {
		job.FileDone()
	}
}

type renameEntryReq struct {
	Mount   string `json:"mount"`
	Path    string `json:"path"`
	NewName string `json:"newName"`
}

// LocalRename handles POST /api/local/rename — renames a file or folder in
// place (same parent directory). Same access model as delete: a writable mount
// ("meus downloads") or admin. NewName must be a bare file name (no separators,
// no traversal) and the mount root can't be renamed. Reuses movePath +
// relinkMovedTorrents so a renamed download keeps its torrent link and replays.
func LocalRename(b *lb.Browser, dls *downloads.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := bindRenameReq(c)
		if !ok {
			return
		}
		if !CheckMountAccess(b, c, req.Mount) || !canModifyMount(c, req.Mount) {
			return
		}
		srcAbs, stat, ok := resolveRenameSource(b, c, req)
		if !ok {
			return
		}
		dstAbs, ok := resolveRenameDest(c, srcAbs, req.NewName)
		if !ok {
			return
		}
		if err := movePath(srcAbs, dstAbs, stat); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "renomear: " + err.Error()})
			return
		}
		relinked := relinkMovedTorrents(dls, s, srcAbs, dstAbs)
		c.JSON(http.StatusOK, gin.H{"renamed": filepath.Base(dstAbs), "relinked": relinked})
	}
}

// bindRenameReq parses + validates the rename request (required fields and a
// safe bare name). Writes the 400 and returns ok=false on any problem.
func bindRenameReq(c *gin.Context) (renameEntryReq, bool) {
	var req renameEntryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return req, false
	}
	if req.Mount == "" || req.Path == "" || req.NewName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mount, path and newName are required"})
		return req, false
	}
	if !isValidRenameName(req.NewName) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nome inválido: não pode conter barras nem '..'"})
		return req, false
	}
	return req, true
}

// resolveRenameSource resolves the source path (scoped + traversal-guarded) and
// stats it. Writes the error response and returns ok=false on failure.
func resolveRenameSource(b *lb.Browser, c *gin.Context, req renameEntryReq) (string, os.FileInfo, bool) {
	srcAbs, err := resolveDeletablePath(b, req.Mount, ScopePath(b, c, req.Mount, req.Path))
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": errFileOrDirNotFound})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		}
		return "", nil, false
	}
	stat, err := os.Stat(srcAbs)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": errFileOrDirNotFound})
		return "", nil, false
	}
	return srcAbs, stat, true
}

// resolveRenameDest builds the destination path (same parent dir, new bare
// name) and refuses a no-op or a clobber. ok=false on failure (response sent).
func resolveRenameDest(c *gin.Context, srcAbs, newName string) (string, bool) {
	dstAbs := filepath.Join(filepath.Dir(srcAbs), newName)
	if dstAbs == srcAbs {
		c.JSON(http.StatusBadRequest, gin.H{"error": "o novo nome é igual ao atual"})
		return "", false
	}
	if _, err := os.Stat(dstAbs); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "já existe um item com esse nome"})
		return "", false
	}
	return dstAbs, true
}

// isValidRenameName reports whether name is a safe bare file name: not empty,
// not a traversal token, and free of path separators. filepath.Base collapses
// any path to its last element, so equality proves there was no separator to
// begin with (covers both / and the OS separator).
func isValidRenameName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	return filepath.Base(name) == name
}

// movePath handles moving files and directories, even across different filesystems/mounts.
func movePath(src, dst string, stat os.FileInfo) error {
	return MovePathJob(src, dst, stat, nil, 0, 0)
}

// MovePathJob is movePath with transfer-progress reporting. files/bytes are the
// item's precomputed totals, reported in one shot for the instant same-fs rename;
// the cross-device copy fallback meters itself, so those counts are ignored there.
func MovePathJob(src, dst string, stat os.FileInfo, job *transfer.Job, files int, bytes int64) error {
	if err := os.Rename(src, dst); err == nil {
		reportInstantMove(job, files, bytes)
		return nil
	}
	// If rename fails (e.g. cross-device link), copy and delete (metered).
	if stat.IsDir() {
		return copyDirAndRemoveJob(src, dst, stat, job)
	}
	return copyFileAndRemoveJob(src, dst, stat, job)
}

func copyFileAndRemove(src, dst string, stat os.FileInfo) error {
	return copyFileAndRemoveJob(src, dst, stat, nil)
}

func copyFileAndRemoveJob(src, dst string, stat os.FileInfo, job *transfer.Job) error {
	// Resume: se o destino já tem este arquivo com o MESMO tamanho, uma
	// transferência anterior já o copiou (foi interrompida depois, no meio do
	// lote). Pula a cópia — contabiliza no progresso (sem inflar a taxa) e remove
	// a origem. É o que torna o move/promote retomável sem recopiar o que já foi.
	if di, err := os.Stat(dst); err == nil && !di.IsDir() && di.Size() == stat.Size() {
		job.AddSkipped(stat.Size())
		job.FileDone()
		return os.Remove(src)
	}
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

	if _, err = io.Copy(out, transfer.ProgressReader(in, job.AddBytesFunc())); err != nil {
		_ = os.Remove(dst)
		return err
	}

	_ = out.Close()
	_ = in.Close()
	// Preserve the original mtime — os.Rename keeps it, but this cross-device
	// fallback (→ rclone/GDrive, other disk) would otherwise stamp "now",
	// breaking date sort and mtime-based scans.
	_ = os.Chtimes(dst, stat.ModTime(), stat.ModTime())
	job.FileDone()
	return os.Remove(src)
}

func copyDirAndRemove(src, dst string, stat os.FileInfo) error {
	return copyDirAndRemoveJob(src, dst, stat, nil)
}

func copyDirAndRemoveJob(src, dst string, stat os.FileInfo, job *transfer.Job) error {
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
			if err := copyDirAndRemoveJob(srcPath, dstPath, info, job); err != nil {
				return err
			}
		} else {
			if err := copyFileAndRemoveJob(srcPath, dstPath, info, job); err != nil {
				return err
			}
		}
	}

	// Preserve the directory's mtime too (see copyFileAndRemove).
	_ = os.Chtimes(dst, stat.ModTime(), stat.ModTime())
	// After recursive copying, remove the source directory completely
	return os.RemoveAll(src)
}
