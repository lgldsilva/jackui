package handlers

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/lgldsilva/jackui/internal/downloads"
	lh "github.com/lgldsilva/jackui/internal/handlers/local"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// ReconcilePendingTransfers re-submits transfers (promote/move) that a restart
// interrupted. The intent was persisted before the copy started (see
// submitPromotePlans); here we re-run whatever is still pending. The copy is
// resume-aware (copyFileAndRemove skips files already at the destination), so a
// re-submit only finishes the leftover — it never re-copies what completed.
//
// Called once at boot, AFTER the streamer/downloads stores are up. Each transfer
// goes back through the same transfer pool (bounded concurrency), so a big
// backlog won't thrash the disk.
func ReconcilePendingTransfers(pending *transfer.Store, tr *transfer.Tracker, store *downloads.Store, s *streamer.Streamer) {
	if pending == nil {
		return
	}
	list, err := pending.List()
	if err != nil {
		log.Printf("transfer reconcile: list failed: %v", err)
		return
	}
	if len(list) == 0 {
		return
	}
	log.Printf("transfer reconcile: %d pending transfer(s) to resume after restart", len(list))
	for _, pt := range list {
		switch pt.Kind {
		case "promote":
			reconcilePromote(pending, tr, store, s, pt)
		default:
			// Unknown kind (older row / not yet supported): drop it so it doesn't
			// linger forever. The source file stays intact (nothing was deleted).
			log.Printf("transfer reconcile: dropping unsupported kind %q (#%d)", pt.Kind, pt.ID)
			_ = pending.Remove(pt.ID)
		}
	}
}

// reconcilePromote finishes a promote whose copy was cut off by a restart.
func reconcilePromote(pending *transfer.Store, tr *transfer.Tracker, store *downloads.Store, s *streamer.Streamer, pt transfer.Pending) {
	var pl promotePayload
	_ = json.Unmarshal([]byte(pt.Payload), &pl)

	info, statErr := os.Stat(pt.Src)
	if statErr != nil {
		// Source gone: either the copy actually finished (then the row was about
		// to be removed) or it was lost. If the destination exists, treat as done
		// and re-point the download row; either way clear the pending entry.
		if _, dstErr := os.Stat(pt.Dst); dstErr == nil && pl.DownloadID > 0 {
			_ = store.SetFilePath(pl.UserID, pl.DownloadID, pt.Dst)
		}
		_ = pending.Remove(pt.ID)
		return
	}

	// Rebuild the download (for InfoHash → re-seed) when still present; fall back
	// to a bare row so the copy + SetFilePath still run without re-seeding.
	d, _ := store.Get(pl.UserID, pl.DownloadID)
	if d == nil {
		d = &downloads.Download{ID: pl.DownloadID}
	}
	// Garante o diretório destino (no fluxo normal o ensureTargetDir cuida disso
	// na fase de preparação; aqui re-submetemos direto a cópia).
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	if err := os.MkdirAll(filepath.Dir(pt.Dst), 0o755); err != nil {
		log.Printf("transfer reconcile: mkdir dst for #%d failed: %v", pl.DownloadID, err)
		return
	}
	files, bytes := lh.CountTree(pt.Src)
	o := &promoteOpts{store: store, s: s, userID: pl.UserID, keepSeeding: pl.KeepSeeding, tracker: tr, pending: pending}
	p := &promotePlan{d: d, src: pt.Src, dst: pt.Dst, srcInfo: info, files: files, bytes: bytes}
	label := safeBaseName(pt.Src, d.Name)
	tr.Submit(label, "promote", files, bytes, func(job *transfer.Job) {
		if err := runPromotePlan(o, p, job); err != nil {
			job.Fail(err)
			log.Printf("transfer reconcile: resume promote #%d failed: %v", pl.DownloadID, err)
			return
		}
		_ = pending.Remove(pt.ID)
		job.Done()
	})
}
