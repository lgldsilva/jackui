package downloads

import (
	"context"
	"log"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// startGroupInit launches ONE init goroutine for an entire torrent group: it
// resolves the magnet + waits for metadata ONCE, then marks every SELECTED file
// wanted (file priorities) and cancels the rest, tracking each member. A single
// member (single-file or whole-torrent) reuses the existing per-row init so those
// paths — and their tests — are byte-for-byte unchanged.
func (w *Worker) startGroupInit(g Group) {
	if len(g.Members) == 1 {
		w.startInit(g.Members[0]) // single-file or whole-torrent: unchanged path
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.registerGroupPending(g, cancel)
	w.doneWG.Add(1)
	go w.initGroup(ctx, g)
}

// registerGroupPending records the shared init-cancel under EVERY member's id, so
// the tick cleanup / Remove / preempt path can abort the group init by cancelling
// any one member (cancel is idempotent). clearGroupPending removes the entries
// when the init goroutine exits.
func (w *Worker) registerGroupPending(g Group, cancel context.CancelFunc) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, m := range g.Members {
		w.setPendingLocked(m.ID, m.InfoHash, cancel)
	}
}

// initGroup resolves a multi-file selected group's torrent ONCE, applies file
// priorities (selected → Download, the rest → Cancel) with a single dedup'd
// piece verify, and tracks every selected member. Runs in its own goroutine so a
// slow swarm never blocks the tick. On EnsureActive/metadata failure the whole
// group is retried/failed via the representative row.
func (w *Worker) initGroup(ctx context.Context, g Group) {
	defer w.doneWG.Done()
	defer w.clearGroupPending(g)

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	rep := g.Members[0]
	hash, err := w.ensureActiveWithFallback(ctx, &rep)
	if err != nil {
		w.failOrRetryGroup(g, "load torrent: "+err.Error())
		return
	}
	t, ok := w.streamer.Client().Torrent(hash)
	if !ok {
		w.failOrRetryGroup(g, "torrent gone after EnsureActive")
		return
	}
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		w.failOrRetryGroup(g, "timeout aguardando metadados")
		return
	}
	w.applyFilePriorities(g, hash, t)
}

// applyFilePriorities marks the group's SELECTED files wanted (Download) and the
// rest unwanted (Cancel), verifying only the selected files' pieces once each
// (the streamer dedups per (hash,file)), then persists metadata + tracks each
// selected member. ONE EnsureActive (already done) + selected-only verifies —
// never an N-file whole-torrent re-hash.
func (w *Worker) applyFilePriorities(g Group, hash metainfo.Hash, t *torrent.Torrent) {
	files := t.Files()
	name := t.Name()
	selected := make(map[int]bool, len(g.Members))
	for _, m := range g.Members {
		// A member removed while this shared init was resolving metadata must NOT be
		// marked wanted / tracked — that would resurrect a deleted row and fetch a
		// file the user just cancelled. The tombstone is the authoritative guard.
		if w.isRemoved(m.ID) {
			continue
		}
		idx, ok := w.resolveFileIndex(&m, files)
		if !ok {
			continue
		}
		selected[idx] = true
		if err := w.streamer.VerifyFile(hash, idx); err != nil {
			log.Printf("downloads: verify selected file #%d: %v", m.ID, err)
		}
		files[idx].Download()
		w.persistMemberMetadata(m, t, files[idx], name)
		w.trackMember(m, t, files[idx], nil, name, hash)
	}
	// Everything NOT selected stays unwanted (PiecePriorityNone) so anacrolix never
	// fetches it — the whole point of per-file selection on a multi-file torrent.
	for i, f := range files {
		if !selected[i] {
			f.Cancel()
		}
	}
}

// persistMemberMetadata writes a member row's resolved name/path/size and freezes
// its completion destination (mirrors initDownload's persistence for one row).
func (w *Worker) persistMemberMetadata(d Download, t *torrent.Torrent, f *torrent.File, name string) {
	filePath, fileSize := w.initFilePath(d, t, f, name)
	if err := w.store.UpdateMetadata(d.UserID, d.ID, name, filePath, fileSize); err != nil {
		log.Printf("downloads: update metadata #%d: %v", d.ID, err)
	}
	if dest := w.completionDest(d, name); dest != "" {
		if err := w.store.SetCompletionDest(d.UserID, d.ID, dest); err != nil {
			log.Printf("downloads: set completion_dest #%d: %v", d.ID, err)
		}
	}
}

// clearGroupPending removes every member's pending entry and deletion tombstone
// once the group init goroutine exits (mirrors initDownload's deferred cleanup,
// per-id so a reused id starts clean).
func (w *Worker) clearGroupPending(g Group) {
	w.mu.Lock()
	for _, m := range g.Members {
		w.clearPendingLocked(m.ID)
		delete(w.removed, m.ID)
	}
	w.mu.Unlock()
}

// failOrRetryGroup applies failOrRetry to every member so a transient group-init
// failure retries on the next tick (or fails the whole group at the cap).
func (w *Worker) failOrRetryGroup(g Group, msg string) {
	for _, m := range g.Members {
		w.failOrRetry(m, msg)
	}
}

// adoptSiblings marks any selected member NOT yet tracked onto the group's
// already-live torrent: it claims that single file (VerifyFile dedupes per
// (hash,file) inside the streamer, so no redundant whole-torrent re-hash) and
// tracks it. Whole-torrent groups have a single member that the init already
// handled, so there's nothing to adopt. This is the cheap O(new files) path that
// replaces a full per-row init for every sibling.
func (w *Worker) adoptSiblings(g Group, state groupState) {
	if g.isWhole() || state.torrent == nil {
		return
	}
	files := state.torrent.Files()
	hash := state.torrent.InfoHash()
	name := state.torrent.Name()
	for _, m := range g.Members {
		if _, ok := state.tracked[m.ID]; ok {
			continue
		}
		idx, okIdx := w.resolveFileIndex(&m, files)
		if !okIdx {
			continue
		}
		if err := w.streamer.VerifyFile(hash, idx); err != nil {
			log.Printf("downloads: verify adopted file #%d: %v", m.ID, err)
		}
		files[idx].Download()
		w.trackMember(m, state.torrent, files[idx], nil, name, hash)
	}
}

// isRemoved reports whether a download ID carries a deletion tombstone (Remove()
// landed while a shared init was in flight). Takes w.mu briefly.
func (w *Worker) isRemoved(id int) bool {
	w.mu.Lock()
	_, tomb := w.removed[id]
	w.mu.Unlock()
	return tomb
}

// trackMember registers eviction protection and adds a member's trackedDL under
// w.mu, honoring the deletion tombstone so a row removed mid-tick isn't
// resurrected. Exactly one of file/whole is set.
func (w *Worker) trackMember(d Download, t *torrent.Torrent, f *torrent.File, whole wholeTarget, name string, hash metainfo.Hash) {
	w.streamer.RegisterDownload(name)
	now := time.Now()
	td := &trackedDL{
		id: d.ID, userID: d.UserID, infoHash: d.InfoHash, hash: hash,
		torrent: t, file: f, whole: whole, name: name,
		startedAt: now, lastProgressAt: now,
	}
	td.lastProgressBytes, _, _ = td.progress()
	w.mu.Lock()
	if _, tomb := w.removed[d.ID]; tomb {
		w.mu.Unlock()
		w.unregisterByName(name)
		return
	}
	w.tracked[d.ID] = td
	delete(w.retries, d.ID)
	w.mu.Unlock()
	if b, _, _ := td.progress(); b > 0 {
		if err := w.store.UpdateProgress(d.UserID, d.ID, b); err != nil {
			log.Printf("downloads: initial progress #%d: %v", d.ID, err)
		}
	}
}

// unregisterByName drops eviction protection for a torrent name unless a tracked
// sibling still needs it (mirrors unregisterLocked but by name, for the abort
// path where there's no trackedDL to pass).
func (w *Worker) unregisterByName(name string) {
	w.mu.Lock()
	for _, other := range w.tracked {
		if other.name == name {
			w.mu.Unlock()
			return
		}
	}
	w.mu.Unlock()
	w.streamer.UnregisterDownload(name)
}
