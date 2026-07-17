package downloads

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/streamer"
)

// Init: arranque do torrent (initDownload/ensureActive/fallback) — extraído de worker.go.
// initDownload resolves the magnet, waits for metadata, marks the target file
// for full download, and (on success) promotes the row into `tracked`. Runs in
// its own goroutine so a slow swarm never blocks the tick loop. Transient
// failures are retried on later ticks up to maxInitRetries; a context cancel
// (download paused/cancelled, or worker stopping) silently aborts without
// touching the row's status.
func (w *Worker) initDownload(ctx context.Context, d Download) {
	defer w.doneWG.Done()
	defer func() {
		w.mu.Lock()
		w.clearPendingLocked(d.ID)
		// Clear any deletion tombstone now that THIS init has fully exited: the
		// resurrection window is closed (we either bailed or promoted under the
		// lock above), so an ID reused by a later Create starts clean. Done here
		// (always-run defer) so the tombstone never leaks when init bails before
		// the promotion guard (e.g. EnsureActive failed).
		delete(w.removed, d.ID)
		w.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	// EffectiveMagnet is the active alternative source when rotation has switched
	// away from the original, otherwise the original magnet. A common failure is
	// an ephemeral indexer .torrent URL (Jackett /dl/...) that has since 404'd —
	// ensureActiveWithFallback recovers via a bare info_hash magnet.
	hash, err := w.ensureActiveWithFallback(ctx, &d)
	if err != nil {
		w.failOrRetry(d, "load torrent: "+err.Error())
		return
	}
	t, ok := w.streamer.Client().Torrent(hash)
	if !ok {
		w.failOrRetry(d, "torrent gone after EnsureActive")
		return
	}
	// Block waiting for metadata so the file slice is populated. ctx already
	// carries the 90s deadline, so we lean on it instead of a second timer.
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		w.failOrRetry(d, "timeout aguardando metadados")
		return
	}

	f, whole, ok := w.initTarget(&d, hash, t)
	if !ok {
		return
	}

	name := t.Name()
	w.streamer.RegisterDownload(name)
	// Persist resolved torrent metadata. file_path GRAVA ABSOLUTO (dataDir + path
	// dentro do torrent) — não relativo. Antes guardava só `f.Path()` (relativo,
	// ex.: "Folder/file.mkv"); se o move pós-completion falhava (cross-mount,
	// container OOM no meio do copy), o file_path ficava inválido pra qualquer
	// consumer (Local browser, Promote, etc.). Absoluto: se move sucede,
	// SetFilePath sobrescreve com o destino; se falha, ainda dá pra achar o
	// arquivo na cache pelo path. Whole-torrent: a raiz do torrent na cache e o
	// tamanho agregado.
	filePath, fileSize := w.initFilePath(d, t, f, name)
	if err := w.store.UpdateMetadata(d.UserID, d.ID, name, filePath, fileSize); err != nil {
		log.Printf("downloads: failed to update metadata for download %d: %v", d.ID, err)
	}
	// Freeze the destination dir now that the name is known — completionBaseDir's
	// inputs (category grouping, auto-promote) can change before completion, and a
	// drift there was what wedged in-flight rows in `moving` after category grouping
	// shipped. The finalize prefers this frozen value over recomputing.
	if dest := w.completionDest(d, name); dest != "" {
		if err := w.store.SetCompletionDest(d.UserID, d.ID, dest); err != nil {
			log.Printf("downloads: failed to set completion_dest for download %d: %v", d.ID, err)
		}
	}

	now := time.Now()
	td := &trackedDL{
		id:             d.ID,
		userID:         d.UserID,
		infoHash:       d.InfoHash,
		hash:           hash,
		torrent:        t,
		file:           f,
		whole:          whole,
		name:           name,
		startedAt:      now,
		lastProgressAt: now,
	}
	td.lastProgressBytes, _, _ = td.progress()
	if !w.promoteOrAbort(d, td, name) {
		return
	}
	// Snapshot inicial dos bytes já completos. Sem isso, o usuário que clica
	// Download enquanto está streamando vê 0% nos primeiros 2-4s (entre o
	// init terminar e o primeiro tick rodar UpdateProgress) — interpreta como
	// "recomeçou". VerifyFile acima já reconciliou o estado de pieces, então
	// BytesCompleted aqui reflete a realidade do disco.
	initialBytes, totalBytes, _ := td.progress()
	if initialBytes > 0 {
		if err := w.store.UpdateProgress(d.UserID, d.ID, initialBytes); err != nil {
			log.Printf("downloads: failed to update initial progress for download %d: %v", d.ID, err)
		}
	}
	log.Printf("downloads: started #%d %q (file %d, %d bytes, completed=%d)", d.ID, name, d.FileIndex, totalBytes, initialBytes)
}

// promoteOrAbort moves a freshly-initialized download into `tracked` UNLESS it
// was cancelled or deleted while init was resolving metadata. Returns false
// (without promoting) in two cases:
//
//   - `pending` no longer holds our entry: the tick loop or Remove() deleted it
//     and called cancel (paused/cancelled/preempted/deleted).
//   - `removed` holds a tombstone: Remove() deleted the row while we were
//     resolving metadata. Re-promoting here would RESURRECT a row the user just
//     deleted — the intermittent "Remove didn't remove" window this fix closes.
//
// On abort it undoes the eviction protection initDownload speculatively
// registered. The tombstone itself is cleared by initDownload's deferred
// cleanup once this goroutine exits.
func (w *Worker) promoteOrAbort(d Download, td *trackedDL, name string) bool {
	w.mu.Lock()
	_, stillPending := w.pending[d.ID]
	_, tombstoned := w.removed[d.ID]
	if !stillPending || tombstoned {
		w.mu.Unlock()
		w.streamer.UnregisterDownload(name)
		return false
	}
	w.tracked[d.ID] = td
	delete(w.retries, d.ID)
	w.mu.Unlock()
	return true
}

// initTarget marks the row's download target as wanted in anacrolix and
// returns it: a single *torrent.File for per-file rows, or the torrent itself
// (as a wholeTarget) for FileIndexWholeTorrent rows. ok=false means the row was
// already flipped to failed (no files in torrent).
//
// Both paths hash-check pieces no disco ANTES de marcar como wanted. Sem isso,
// se o shutdown anterior foi ungraceful (SIGKILL pelo Docker antes do
// graceful-shutdown ficar pronto), o bolt DB do anacrolix está stale — pieces
// existem no disco mas anacrolix os marca como incompletos e pediria esses
// bytes do swarm de novo. VerifyFile/VerifyTorrent hasheiam cada piece e marcam
// como Complete os que casam (idempotente, dedupe por processo).
func (w *Worker) initTarget(d *Download, hash metainfo.Hash, t wholeTarget) (*torrent.File, wholeTarget, bool) {
	if d.IsWholeTorrent() {
		if err := w.streamer.VerifyTorrent(hash); err != nil {
			log.Printf("downloads: verify torrent (structural error) for download %d: %v", d.ID, err)
		}
		// DownloadAll sets piece priority to Normal across the whole torrent —
		// anacrolix schedules every file to completion. ONE queue row, ONE slot.
		t.DownloadAll()
		return nil, t, true
	}
	files := t.Files()
	fileIdx, okResolved := w.resolveFileIndex(d, files)
	if !okResolved {
		return nil, nil, false
	}
	f := files[fileIdx]
	// Cross-torrent dedup (#23): if this exact file is already on disk from another
	// completed download, adopt it (link) instead of re-downloading from the swarm.
	// ok=false makes the caller return without tracking; the row is already a
	// completed link, and the idle torrent (no piece wanted) is evicted normally.
	if w.tryLinkExisting(d, hash, fileIdx, f) {
		return nil, nil, false
	}
	if err := w.streamer.VerifyFile(hash, fileIdx); err != nil {
		log.Printf("downloads: verify file (structural error) for download %d: %v", d.ID, err)
	}
	// File.Download() sets piece priority to Normal across the file's piece
	// range — anacrolix then schedules a full download to completion.
	f.Download()
	return f, nil, true
}

// resolveFileIndex resolves the target file index in a torrent. If index is out of bounds,
// it auto-picks the best file. Returns the resolved index and true on success.
func (w *Worker) resolveFileIndex(d *Download, files []*torrent.File) (int, bool) {
	fileIdx := d.FileIndex
	if fileIdx >= 0 && fileIdx < len(files) {
		return fileIdx, true
	}
	// Auto-pick: FileIndex == -1 means "pick the best file".
	// We prefer the largest video/media file, or fall back to the largest file overall.
	fileIdx = pickBestFile(files)
	if fileIdx < 0 {
		if err := w.store.SetError(d.UserID, d.ID, "no files in torrent"); err != nil {
			log.Printf("downloads: failed to set error status for download %d: %v", d.ID, err)
		}
		return -1, false
	}
	// Persist the resolved FileIndex so subsequent ticks don't re-pick.
	if d.FileIndex != fileIdx {
		if err := w.store.SetFileIndex(d.UserID, d.ID, fileIdx); err != nil {
			log.Printf("downloads: failed to set file index for download %d: %v", d.ID, err)
		}
		d.FileIndex = fileIdx
	}
	return fileIdx, true
}

// ensureActiveWithFallback loads the torrent for a download, recovering from a
// dead primary source. Indexer .torrent links (Jackett /dl/...) are ephemeral —
// once the token/cache expires they 404, and a row whose stored "magnet" is
// actually such a URL would fail init forever. When that happens and the
// info_hash is known, we retry with a bare magnet (DHT + the streamer's injected
// public trackers resolve it) and persist it so later retries/reboots skip the
// dead URL.
func (w *Worker) ensureActiveWithFallback(ctx context.Context, d *Download) (metainfo.Hash, error) {
	src := d.EffectiveMagnet()
	hash, err := w.ensureActive(ctx, *d, src)
	if err == nil {
		return hash, nil
	}
	alt, ok := fallbackMagnet(src, d.InfoHash)
	if !ok {
		return hash, err
	}
	log.Printf("downloads: #%d source failed (%v) — retrying via info_hash magnet", d.ID, err)
	h2, err2 := w.ensureActive(ctx, *d, alt)
	if err2 != nil {
		return hash, fmt.Errorf("%v; fallback por info_hash também falhou: %w", err, err2)
	}
	if uerr := w.store.SetActiveMagnet(d.UserID, d.ID, alt); uerr != nil {
		log.Printf("downloads: #%d persist fallback magnet failed: %v", d.ID, uerr)
	} else {
		d.ActiveMagnet = alt
	}
	return h2, nil
}

// ensureActive adds the torrent, writing its data DIRECTLY to the configured
// bulk destination (download-to-bulk) when one is set — so torrents larger than
// the SSD cache don't overflow it and the move-on-completion is a no-op. With no
// destination configured it falls back to the cache (legacy streaming storage).
// The BaseDir is the destination PARENT (without the torrent-name segment); the
// storage appends <sanitizeFolderName(name)> itself once metadata resolves the
// real name — keeping the write path identical to completionDest.
func (w *Worker) ensureActive(ctx context.Context, d Download, src string) (metainfo.Hash, error) {
	base := w.completionBaseDir(d)
	if base == "" {
		return w.streamer.EnsureActive(ctx, src)
	}
	return w.streamer.EnsureActiveForDownload(ctx, src, streamer.DownloadStorageSpec{
		BaseDir:  base,
		Sanitize: sanitizeFolderName,
	})
}

// fallbackMagnet returns a bare info_hash magnet when src is an http(s) URL (an
// ephemeral indexer .torrent link) and a 40-hex info_hash is known. ok is false
// when no fallback applies — src is already a magnet, or the hash is missing.
func fallbackMagnet(src, infoHash string) (magnet string, ok bool) {
	if infoHash == "" {
		return "", false
	}
	low := strings.ToLower(strings.TrimSpace(src))
	if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
		return "", false
	}
	return "magnet:?xt=urn:btih:" + infoHash, true
}
