package downloads

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/lgldsilva/jackui/internal/transfer"
)

// checkGroupCompletion fires the post-download move ONCE for a per-file selected
// torrent when EVERY wanted file is on disk. On a partial group (some files still
// downloading, or a wanted row not yet active) it's a no-op — the move waits
// until the torrent is fully done, so there's one move, one ntfy, one AI-rename
// decision per torrent. Whole-torrent groups never reach here (reconcileGroup
// routes them through the single-row whole path).
func (w *Worker) checkGroupCompletion(g Group, state groupState) {
	if !w.groupCompletionEligible(g, state) {
		return
	}
	w.completeGroup(g, state)
}

// groupCompletionEligible reports whether the torrent is fully done and safe to
// finalize. TWO conditions must hold:
//
//  1. Every member the tick GROUPED (the active/downloading rows) is tracked and
//     has all its bytes on disk (groupAllComplete).
//  2. The torrent has NO other WANTED row still pending in the store — a sibling
//     file the user selected that's `queued` (not yet promoted) or `downloading`
//     but absent from THIS group's snapshot. Without this, the tick — which only
//     sees `downloading` rows (ListActive) — would finalize the torrent while a
//     queued sibling's file is still missing (the premature-completion bug).
func (w *Worker) groupCompletionEligible(g Group, state groupState) bool {
	if !w.groupAllComplete(g, state) {
		return false
	}
	hash := g.Members[0].InfoHash
	if hash == "" {
		return true // hashless pre-metadata row: no siblings to wait on
	}
	siblings, err := w.store.WantedRowsByHash(g.Members[0].UserID, hash)
	if err != nil {
		log.Printf("downloads: completion sibling check for %s failed: %v", g.Key, err)
		return false // be conservative — don't finalize on a read error
	}
	inGroup := make(map[int]bool, len(g.Members))
	for _, m := range g.Members {
		inGroup[m.ID] = true
	}
	for _, s := range siblings {
		if s.IsWholeTorrent() {
			continue // a whole row is its own group; not part of this per-file set
		}
		if !inGroup[s.ID] {
			return false // a wanted sibling outside this group's snapshot → wait
		}
	}
	return true
}

// groupAllComplete reports whether every selected member of the group is tracked
// AND has all its bytes on disk. A not-yet-tracked or still-downloading member
// keeps the group incomplete.
func (w *Worker) groupAllComplete(g Group, state groupState) bool {
	for _, m := range g.Members {
		td := state.tracked[m.ID]
		if td == nil {
			return false
		}
		completed, total, ok := td.progress()
		if !ok || total <= 0 || completed < total {
			return false
		}
	}
	return true
}

// groupMover is one transitioned member's move plan: the download row plus its
// torrent-relative path and byte length (captured before untracking).
type groupMover struct {
	row     Download
	relPath string
	length  int64
}

// completeGroup runs the post-download move ONCE for a multi-file selected
// torrent. It flips the DOWNLOADING rows to `moving` with a STATUS-GUARDED batch
// (a row paused/removed between the tick snapshot and now is skipped, not
// clobbered), captures the move plan for ONLY the rows that transitioned,
// untracks them (keeping eviction protection alive for the copy), and submits ONE
// transfer job. Multi-file groups skip AI-rename (the chain targets one media
// file, like whole-torrent). A single-file group falls through to the per-row
// checkCompletion so its single-file move + AI-rename path is unchanged.
func (w *Worker) completeGroup(g Group, state groupState) {
	if len(g.Members) == 1 {
		w.checkCompletion(g.Members[0], state.tracked[g.Members[0].ID])
		return
	}
	moved, err := w.store.MoveGroup(g.memberIDs())
	if err != nil {
		log.Printf("downloads: set group %s moving: %v", g.Key, err)
		return // stays downloading; next tick retries the hand-off
	}
	if len(moved) == 0 {
		return // nothing transitioned (all paused/removed) — nothing to move
	}
	movedSet := make(map[int]bool, len(moved))
	for _, id := range moved {
		movedSet[id] = true
	}
	rep := g.Members[0]
	name := state.tracked[rep.ID].name
	movers := make([]groupMover, 0, len(moved))
	var total int64
	for _, m := range g.Members {
		mt := state.tracked[m.ID]
		if !movedSet[m.ID] || mt == nil || mt.file == nil {
			continue
		}
		movers = append(movers, groupMover{row: m, relPath: mt.file.Path(), length: mt.file.Length()})
		total += mt.file.Length()
	}
	w.mu.Lock()
	for _, id := range moved {
		delete(w.tracked, id)
	}
	w.mu.Unlock()
	// Group members share one owner; use the first member's userID for the dock.
	owner := 0
	if len(movers) > 0 {
		owner = movers[0].row.UserID
	}
	w.tracker.SubmitFor(owner, name, "download-move", len(movers), total, func(job *transfer.Job) {
		w.runGroupCompletionMove(g, name, movers, total, job)
	})
}

// runGroupCompletionMove relocates each selected file to its OWN destination
// (preserving the torrent's internal tree) off the tick, sets EACH member's
// file_path to its own moved file (item 6: never the bare torrent dir), then
// finalizes the moved rows to `completed` (status-guarded). On a persistent move
// error (after moveMaxAttempts retries) the moved rows go `failed`; an app
// shutdown mid-retry leaves them `moving` for boot rescue. Multi-file groups
// never AI-rename.
func (w *Worker) runGroupCompletionMove(g Group, name string, movers []groupMover, total int64, job *transfer.Job) {
	var err error
	for attempt := 1; attempt <= moveMaxAttempts; attempt++ {
		if job.Canceled() {
			err = fmt.Errorf("transferência cancelada")
			break
		}
		err = w.moveGroupFiles(g, name, movers, job)
		if err == nil {
			break
		}
		log.Printf("downloads: group %s move attempt %d/%d: %v", g.Key, attempt, moveMaxAttempts, err)
		if attempt == moveMaxAttempts {
			break
		}
		select {
		case <-w.stop:
			return // shutting down: leave rows `moving` for boot rescue
		case <-time.After(time.Duration(attempt) * w.moveBackoff):
		}
	}
	if err != nil {
		for _, mv := range movers {
			if e := w.store.SetError(mv.row.UserID, mv.row.ID, "move failed: "+err.Error()); e != nil {
				log.Printf("downloads: mark group move-failed #%d: %v", mv.row.ID, e)
			}
		}
		job.Fail(err)
		log.Printf("downloads: group %s move failed: %v", g.Key, err)
		return
	}
	ids := make([]int, 0, len(movers))
	for _, mv := range movers {
		ids = append(ids, mv.row.ID)
	}
	if _, err := w.store.CompleteGroup(ids); err != nil {
		log.Printf("downloads: set group %s completed: %v", g.Key, err)
	}
	w.streamer.UnregisterDownload(name)
	job.Done()
	log.Printf("downloads: completed group %s %q (%d files)", g.Key, name, len(movers))
	// Whole-torrent groups skip AI-rename (renamed=false): the rename chain targets
	// a single media file, not a tree — so there's no moved-file handle to release.
	go w.reseedAfterCompletion(g.Members[0], false)
	body := fmt.Sprintf("%s · %d arquivos · %.2f MB", name, len(movers), float64(total)/1048576)
	go w.sendNtfy(context.Background(), "Download concluído: "+name, body, "white_check_mark,torrent")
}

// moveGroupFiles relocates each selected file from the cache into its per-torrent
// destination, preserving the torrent's internal tree, and records EACH member's
// own destination path (item 6 fix: a member never ends up pointing at the bare
// torrent directory). Idempotent per file (moveTreeEntry skips a file already at
// the destination — covers download-to-bulk, where the data was written straight
// to the dest and the "move" is a no-op). A canceled job or a missing file aborts
// with an error so runGroupCompletionMove marks the group failed.
func (w *Worker) moveGroupFiles(g Group, name string, movers []groupMover, job *transfer.Job) error {
	destDir := w.completionDest(g.Members[0], name)
	if destDir == "" {
		return nil // legacy mode (no destination configured): keep files in DataDir
	}
	for _, mv := range movers {
		if job.Canceled() {
			return fmt.Errorf("transferência cancelada")
		}
		moved, err := moveTreeEntry(job.Context(), w.dataDir, destDir, name, mv.relPath, job.AddBytesFunc())
		if err != nil {
			return err
		}
		if !moved {
			continue // BEP 47 pad entry — nothing to record
		}
		dst, err := wholeTorrentDest(destDir, name, mv.relPath)
		if err != nil {
			return err
		}
		if e := w.store.SetFilePath(mv.row.UserID, mv.row.ID, dst); e != nil {
			log.Printf("downloads: set file_path #%d: %v", mv.row.ID, e)
		}
		job.FileDone()
	}
	return nil
}
