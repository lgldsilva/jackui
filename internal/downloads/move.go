package downloads

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/renamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// moveMaxAttempts bounds in-process retries of a post-download move before the
// row is marked `failed`. Without a bound a recurring error (e.g. the
// `permission denied` we saw on the destination) would retry forever, leaving the
// download wedged at "100% downloading". A move interrupted by app shutdown is
// NOT a failure: the row stays `moving` and boot rescue re-dispatches it.
const moveMaxAttempts = 3

// runCompletionMove performs the post-download relocation OFF the tick loop and
// finalizes the row. The move helpers are idempotent, so each retry resumes where
// the last left off. On success → `completed` (+ AI rename + ntfy); after
// moveMaxAttempts of a persistent error → `failed` with the message; on app
// shutdown mid-retry it returns leaving the row `moving` for boot rescue.
func (w *Worker) runCompletionMove(d Download, name string, relPaths []string, whole bool, total int64, job *transfer.Job) {
	dst, err := w.attemptCompletionMove(d, name, relPaths, whole, job)
	if err != nil {
		if e := w.store.SetError(d.UserID, d.ID, "move failed: "+err.Error()); e != nil {
			log.Printf("downloads: failed to mark move-failed #%d: %v", d.ID, e)
		}
		job.Fail(err)
		log.Printf("downloads: completion move #%d %q failed after %d attempts: %v", d.ID, name, moveMaxAttempts, err)
		return
	}
	if err := w.store.SetStatus(d.UserID, d.ID, StatusCompleted); err != nil {
		log.Printf("downloads: failed to set status completed for download %d: %v", d.ID, err)
	}
	job.Done()
	log.Printf("downloads: completed #%d %q", d.ID, name)
	body := fmt.Sprintf("%s · %.2f MB", name, float64(total)/1048576)
	go w.sendNtfy(context.Background(), "Download concluído: "+name, body, "white_check_mark,torrent")
	// ORDER MATTERS: AI-rename BEFORE reseed. Both touch the same on-disk file —
	// the rename moves it, the reseed reopens the torrent on it. Running them
	// concurrently (the old `go ...; go ...`) raced: the reseed reopened the
	// torrent pointing at the cache/bulk path, then the rename moved the file out
	// from under it, leaving anacrolix holding an fd+mmap on the now-(deleted)
	// inode — the kernel can't reclaim those pages, so a single 2 GB file pinned
	// ~1.8 GB of RSS until the process dropped the torrent or restarted. Renaming
	// first (and persisting the new path via SetFilePath) lets the reseed's
	// relocatedStorage resolve the FINAL location. Runs inline: runCompletionMove
	// is already off the tick loop, in its own goroutine.
	renamed := false
	// AI auto-rename (Plex-style) when configured. Whole-torrent rows skip it:
	// the rename chain targets ONE media file, not a tree of N files.
	if w.aiClient != nil && dst != "" && !whole {
		if nd := w.aiRenameCompleted(d, dst); nd != "" {
			d.FilePath = nd
			renamed = true
		}
	}
	// Seed-tracker content keeps seeding from its NEW (bulk/renamed) home instead
	// of going idle: the download torrent still points at the now-moved cache file,
	// so we swap it onto the relocated storage. Status is `completed` + file_path
	// updated by now, so EnsureActive's relocatedStorage resolves to the real file.
	// For non-seed downloads that were renamed, reseedAfterCompletion still drops
	// the torrent so the stale handle on the moved file is released.
	w.reseedAfterCompletion(d, renamed)
}

// reseedAfterCompletion re-activates a just-completed download from its new bulk
// location when its tracker is configured for continuous seeding. The torrent
// that drove the download still has cache-rooted storage pointing at the file we
// just moved away, so Drop + EnsureActive swaps it onto the relocated storage
// (anacrolix verifies the bulk file and seeds — no re-download). No-op when the
// tracker isn't a seed-tracker — EXCEPT that when the file was just renamed
// (`renamed`), it still Drops the torrent so the fd/mmap it holds on the moved
// file is released (otherwise the (deleted) inode keeps pinning RSS).
func (w *Worker) reseedAfterCompletion(d Download, renamed bool) {
	if d.InfoHash == "" {
		return
	}
	var h metainfo.Hash
	if err := h.FromHexString(d.InfoHash); err != nil {
		return
	}
	if !w.streamer.MatchesSeedTrackerCached(h) {
		if renamed {
			// Not a seed-tracker, but the file was just moved by AI-rename: the
			// download torrent still holds an fd/mmap on the old path. Drop it so
			// the (deleted) inode stops pinning RSS. No EnsureActive — we don't seed.
			w.dropTorrent(h)
		}
		return
	}
	w.streamer.Drop(h)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := w.streamer.EnsureActive(ctx, d.SeedSource()); err != nil {
		log.Printf("downloads: reseed #%d %q failed: %v", d.ID, d.Name, err)
		return
	}
	log.Printf("downloads: #%d %q reseeding from bulk (seed-tracker)", d.ID, d.Name)
}

// attemptCompletionMove runs the move with bounded retries, reporting progress to
// job. It returns the destination path on success, or the last error after
// moveMaxAttempts. A shutdown signal (w.stop) aborts the retry loop early with
// the pending error so the caller leaves the row `moving` for boot rescue.
func (w *Worker) attemptCompletionMove(d Download, name string, relPaths []string, whole bool, job *transfer.Job) (string, error) {
	// Download-to-bulk: when the data was written STRAIGHT to its final
	// destination, there's nothing to move — finalize in place. Covers the
	// selected-file-in-multi case too (the storage preserves the internal tree,
	// which moveDownloadedFile would flatten). Falls through to the move when the
	// data ISN'T at the bulk path (no destination configured, or a legacy cache
	// download from before this change).
	if dst, ok := w.tryFinalizeBulk(d, name, relPaths, whole); ok {
		return dst, nil
	}
	var dst string
	var err error
	for attempt := 1; attempt <= moveMaxAttempts; attempt++ {
		if job.Canceled() {
			return "", fmt.Errorf("transferência cancelada")
		}
		if whole {
			dst, err = w.moveCompletedTorrentFiles(d, name, relPaths, job)
		} else {
			dst, err = w.moveCompletedFile(d, relPaths[0], name, job)
		}
		if err == nil {
			return dst, nil
		}
		log.Printf("downloads: completion move #%d %q attempt %d/%d: %v", d.ID, name, attempt, moveMaxAttempts, err)
		if attempt == moveMaxAttempts {
			break
		}
		select {
		case <-w.stop:
			return "", err // shutting down: leave the row `moving` for boot rescue
		case <-time.After(time.Duration(attempt) * w.moveBackoff):
		}
	}
	return "", err
}

// tryFinalizeBulk finalizes a download whose data was written DIRECTLY to its
// bulk destination (download-to-bulk): no move, just persist file_path and
// release the eviction guard. Returns ok=false (so the caller falls back to the
// cache→dest move) when no destination is configured OR the data isn't actually
// at the expected bulk path — e.g. a legacy download that landed in the cache
// before this change. dst is the file (or torrent dir) path on ok.
func (w *Worker) tryFinalizeBulk(d Download, name string, relPaths []string, whole bool) (string, bool) {
	if w.completionBaseDir(d) == "" {
		return "", false
	}
	// Probe each candidate destination dir (frozen → current → category-less) and
	// finalize at the first one that actually holds the data. The fallbacks cover
	// rows whose storage wrote BEFORE category grouping shipped: the current
	// completionDest points at .../<category>/<torrent> (empty), so without the
	// category-less probe they wedged with "completed file not found".
	for _, destDir := range w.bulkDestCandidates(d, name) {
		dst := destDir
		if whole {
			if !dirHasFiles(destDir) {
				continue
			}
		} else {
			dst = filepath.Join(destDir, bulkRelPath(name, relPaths[0]))
			if !fileExists(dst) {
				continue
			}
		}
		if err := w.store.SetFilePath(d.UserID, d.ID, dst); err != nil {
			log.Printf(logFmtSetFilePathFailed, d.ID, err)
		}
		w.streamer.UnregisterDownload(name)
		log.Printf("downloads: #%d %q already in bulk (no move) → %s", d.ID, name, dst)
		return dst, true
	}
	return "", false
}

// bulkDestCandidates lists the per-torrent destination dirs to probe when
// finalizing a download-to-bulk row, in priority order:
//  1. the dir FROZEN at metadata-resolve (authoritative — no drift),
//  2. the freshly-computed completionDest (current category/auto-promote),
//  3. a category-LESS variant (covers rows whose storage wrote before category
//     grouping was added — the move-not-found wedge).
//
// Deduped, empties dropped. The category-less entry only appears when category
// grouping is actually in effect (the base ends with the category segment).
func (w *Worker) bulkDestCandidates(d Download, name string) []string {
	var out []string
	add := func(dir string) {
		if dir == "" {
			return
		}
		for _, x := range out {
			if x == dir {
				return
			}
		}
		out = append(out, dir)
	}
	add(d.CompletionDest)
	add(w.completionDest(d, name))
	if base := w.completionBaseDir(d); base != "" {
		if cat := categoryFolder(d.Category); cat != "" && filepath.Base(base) == cat {
			add(filepath.Join(filepath.Dir(base), sanitizeFolderName(name)))
		}
	}
	return out
}

// bulkRelPath mirrors bulkRel for a torrent-relative path string: strips the
// torrent-name root so it matches the download storage layout.
func bulkRelPath(name, rel string) string {
	return filepath.FromSlash(strings.TrimPrefix(rel, name+"/"))
}

// moveDownloadedFile moves the completed file (final or leftover .part) for
// relPath from dataDir into destDir, returning the destination path. The dst
// always uses the final name, never .part. onBytes (nil-safe) receives the bytes
// copied so the caller can report transfer progress.
func moveDownloadedFile(ctx context.Context, dataDir, destDir, relPath string, onBytes func(int64)) (string, error) {
	dst := filepath.Join(destDir, filepath.Base(relPath))
	src := resolveCompletedSrc(dataDir, relPath)
	if src == "" {
		// Source isn't in the cache. If it's already at the destination, the move
		// was done on a previous attempt (or the file was downloaded straight to
		// bulk via relocated storage) — idempotent success, NOT an error. Mirrors
		// the whole-torrent path (moveTreeEntry); without it, a single-file
		// completed download whose file already lives in bulk wedged the row with
		// "move failed: completed file not found in /data/streams".
		if fileExists(dst) {
			return dst, nil
		}
		// anacrolix relocated storage writes dst+".part" while pieces land and
		// renames it on completion; a torrent dropped before that rename finishes
		// leaves a complete-size .part at the destination. Finishing the rename here
		// recovers the file without re-downloading anything.
		if part := dst + partSuffix; fileExists(part) {
			if err := os.Rename(part, dst); err == nil {
				return dst, nil
			}
		}
		return "", fmt.Errorf("completed file not found in %s for %q", dataDir, relPath)
	}
	// Destination already present: remove the stale cache copy and succeed.
	if fileExists(dst) {
		_ = os.Remove(src)
		return dst, nil
	}
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", destDir, err)
	}
	if err := moveFileProgress(ctx, src, dst, onBytes); err != nil {
		return "", fmt.Errorf("move %s → %s: %w", src, dst, err)
	}
	return dst, nil
}

// moveCompletedFile relocates a finished download from the streaming cache to the
// dedicated downloadDir (per-user, per-torrent folder). Takes the torrent-relative
// path + name as strings (not the *trackedDL) so it stays unit-testable. Returns
// an error (instead of failing silently) so the caller only flips the row to
// "completed" when the file actually reached its home — handling the case where
// the anacrolix storage left a complete ".part" that wasn't renamed yet.
func (w *Worker) moveCompletedFile(d Download, relPath, torrentName string, job *transfer.Job) (string, error) {
	destDir := w.completionDest(d, torrentName)
	if destDir == "" {
		return "", nil
	}
	dst, err := moveDownloadedFile(job.Context(), w.dataDir, destDir, relPath, job.AddBytesFunc())
	if err != nil {
		return "", err
	}
	job.FileDone()
	if err := w.store.SetFilePath(d.UserID, d.ID, dst); err != nil {
		log.Printf(logFmtSetFilePathFailed, d.ID, err)
	}
	w.streamer.UnregisterDownload(torrentName)
	log.Printf("downloads: moved #%d %q → %s", d.ID, torrentName, dst)
	return dst, nil
}

// moveCompletedTorrent relocates EVERY file of a finished whole-torrent
// download from the streaming cache into downloadDir/<user>/<torrent>/,
// preserving the directory structure inside the torrent. Returns the torrent's
// destination directory (persisted as the row's file_path). Same contract as
// moveCompletedFile: an error means nothing was flipped to completed and the
// next tick retries — moveCompletedTree is idempotent, so a retry (or the
// boot-time orphan re-queue) skips files that already reached the destination.
func (w *Worker) moveCompletedTorrent(d Download, td *trackedDL) (string, error) {
	return w.moveCompletedTorrentFiles(d, td.name, wholeTorrentRelPaths(td.whole.Files()), nil)
}

// moveCompletedTorrentFiles is moveCompletedTorrent's core, taking the already-
// resolved torrent-relative paths (so it runs off the tick without the live
// torrent) and a transfer.Job for progress. Same idempotent/error contract.
func (w *Worker) moveCompletedTorrentFiles(d Download, torrentName string, relPaths []string, job *transfer.Job) (string, error) {
	destDir := w.completionDest(d, torrentName)
	if destDir == "" {
		return "", nil
	}
	if err := moveCompletedTree(job.Context(), w.dataDir, destDir, torrentName, relPaths, job.AddBytesFunc(), job.FileDone); err != nil {
		return "", err
	}
	if err := w.store.SetFilePath(d.UserID, d.ID, destDir); err != nil {
		log.Printf(logFmtSetFilePathFailed, d.ID, err)
	}
	w.streamer.UnregisterDownload(torrentName)
	log.Printf("downloads: moved whole torrent #%d %q (%d files) → %s", d.ID, torrentName, len(relPaths), destDir)
	return destDir, nil
}

// moveCompletedTree moves every torrent-relative path from dataDir into
// destDir, keeping the structure inside the torrent. The leading
// "<torrentName>/" segment is stripped (destDir already carries the per-torrent
// folder). Idempotent: a file whose source is gone but whose destination exists
// was moved by a previous (interrupted) attempt and is skipped.
func moveCompletedTree(ctx context.Context, dataDir, destDir, torrentName string, relPaths []string, onBytes func(int64), onFileDone func()) error {
	for _, rel := range relPaths {
		if err := ctx.Err(); err != nil {
			return err // canceled via Tracker.Cancel — stop between files
		}
		moved, err := moveTreeEntry(ctx, dataDir, destDir, torrentName, rel, onBytes)
		if err != nil {
			return err
		}
		if moved && onFileDone != nil {
			onFileDone() // a relocated (or already-present) file counts toward X/Y
		}
	}
	return nil
}

// moveTreeEntry relocates one torrent-relative file into destDir. moved=true when
// a file was moved OR already sat at the destination (a prior attempt); moved=
// false for a skipped BEP 47 pad entry. Idempotent — safe to re-run after an
// interrupted move.
func moveTreeEntry(ctx context.Context, dataDir, destDir, torrentName, rel string, onBytes func(int64)) (bool, error) {
	if isPadPath(torrentName, rel) {
		return false, nil
	}
	dst, err := wholeTorrentDest(destDir, torrentName, rel)
	if err != nil {
		return false, err
	}
	src := resolveCompletedSrc(dataDir, rel)
	if src == "" {
		if fileExists(dst) {
			return true, nil // already moved on a previous attempt
		}
		// anacrolix relocated storage may have written dst+".part" and not yet
		// renamed it (torrent dropped mid-rename). Completing the rename here
		// recovers without re-downloading. Mirrors moveDownloadedFile.
		if part := dst + partSuffix; fileExists(part) {
			if err := os.Rename(part, dst); err == nil {
				return true, nil
			}
		}
		return false, fmt.Errorf("completed file not found in %s for %q", dataDir, rel)
	}
	// Destination already present (e.g. relocated storage wrote it directly to
	// bulk while a cache copy remained). Remove the stale cache file and treat
	// as success — no need to overwrite a read-only (444) relocated-storage file.
	if fileExists(dst) {
		_ = os.Remove(src)
		return true, nil
	}
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, fmt.Errorf("mkdir for %q: %w", rel, err)
	}
	if err := moveFileProgress(ctx, src, dst, onBytes); err != nil {
		return false, fmt.Errorf("move %s → %s: %w", src, dst, err)
	}
	return true, nil
}

// wholeTorrentDest resolves the destination path for one torrent-relative file,
// rejecting metadata-supplied paths that would escape destDir (".." traversal —
// torrent metadata is untrusted input).
func wholeTorrentDest(destDir, torrentName, rel string) (string, error) {
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("unsafe path %q in torrent", rel)
	}
	rel = strings.TrimPrefix(rel, torrentName+"/")
	// Re-validate AFTER the strip: "Name/../x" is lexically local as a whole
	// (it cleans to "x") but escapes destDir once the leading "Name/" is gone.
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("unsafe path %q in torrent", rel)
	}
	return filepath.Join(destDir, filepath.FromSlash(rel)), nil
}

// isPadPath reports whether a torrent-relative path is a BEP 47 padding entry
// by the ".pad/" naming convention (with or without the torrent's root folder
// prefix). Pad files exist only to piece-align the real content and may never
// be materialized on disk — trying to move one would fail every completion
// retry, wedging the download in `downloading` forever.
func isPadPath(torrentName, rel string) bool {
	rel = strings.TrimPrefix(rel, torrentName+"/")
	return strings.HasPrefix(rel, ".pad/")
}

// aiRenameCompleted re-organizes a completed download into a Plex-style path
// under downloadDir, using the AI+TMDB rename chain — the same one the promote
// flow uses. Runs off the tick loop and is best-effort: any failure leaves the
// file where moveCompletedFile already put it. Only invoked when an AI client is
// configured ("se a IA estiver disponível"). Returns the new path on success, or
// "" when nothing was moved (no-op preview, error, or destination == source), so
// the caller knows whether the file was relocated and a stale torrent handle on
// the old path must be released.
func (w *Worker) aiRenameCompleted(d Download, currentPath string) string {
	base := w.downloadDir
	if w.resolveUsername != nil {
		if u := w.resolveUsername(d.UserID); u != "" {
			base = filepath.Join(base, u)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	preview, err := renamer.GeneratePreview(ctx, w.aiClient, w.tmdbClient, filepath.Base(currentPath))
	if err != nil || preview == nil || preview.TargetPath == "" {
		return ""
	}
	targetRel := renamer.ResolveTargetConflict(base, preview.TargetPath)
	newDst := filepath.Join(base, targetRel)
	if newDst == currentPath {
		return ""
	}
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	if err := os.MkdirAll(filepath.Dir(newDst), 0o755); err != nil {
		log.Printf("downloads: AI-rename mkdir #%d: %v", d.ID, err)
		return ""
	}
	var size int64
	if st, e := os.Stat(currentPath); e == nil {
		size = st.Size()
	}
	job := w.tracker.Start(filepath.Base(newDst), "ai-rename", 1, size)
	if err := moveFileProgress(job.Context(), currentPath, newDst, job.AddBytesFunc()); err != nil {
		job.Fail(err)
		log.Printf("downloads: AI-rename move #%d: %v", d.ID, err)
		return ""
	}
	job.FileDone()
	job.Done()
	if err := w.store.SetFilePath(d.UserID, d.ID, newDst); err != nil {
		log.Printf("downloads: AI-rename set path #%d: %v", d.ID, err)
		return ""
	}
	// The per-torrent folder moveCompletedFile created is now empty — tidy it.
	_ = os.Remove(filepath.Dir(currentPath))
	log.Printf("downloads: AI-renamed #%d → %s", d.ID, newDst)
	return newDst
}

// moveFileWithFallback renames src→dst, falling back to copy+remove across
// filesystems (EXDEV). Mirrors the promote move semantics. Delegates to
// moveFileProgress (no progress reporting); aiRenameCompleted uses the metered
// form directly for the Transfers dock.
func moveFileWithFallback(src, dst string) error {
	return moveFileProgress(context.Background(), src, dst, nil)
}

// renameFn is os.Rename, overridable in tests to force the cross-filesystem copy
// fallback (EXDEV can't be reproduced within a single temp dir).
var renameFn = os.Rename

// moveFile moves src to dst with no progress reporting (see moveFileProgress).
func moveFile(src, dst string) error { return moveFileProgress(context.Background(), src, dst, nil) }

// moveFileProgress moves src to dst. Tries os.Rename first (cheap, same-
// filesystem; reports the file size as one chunk so a same-fs move still shows
// 100% on the progress bar); falls back to copy+delete for cross-filesystem moves
// (DataDir on one volume, DownloadDir on another), streaming through a
// transfer.ProgressReader so onBytes (nil-safe) sees the copy advance.
func moveFileProgress(ctx context.Context, src, dst string, onBytes func(int64)) error {
	if err := renameFn(src, dst); err == nil {
		if onBytes != nil {
			if st, e := os.Stat(dst); e == nil {
				onBytes(st.Size())
			}
		}
		return nil
	}
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	// #nosec G304 G302 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna; arquivo de midia; 0644 intencional p/ leitura
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	// Cross-device copy streams through a ctx-aware reader so a Tracker.Cancel
	// aborts it mid-file (the partial dst is removed below).
	if _, err := io.Copy(out, transfer.ProgressReaderCtx(ctx, in, onBytes)); err != nil {
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
