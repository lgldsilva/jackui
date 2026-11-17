package downloads

import (
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// Remove tears down all in-memory state for a deleted download SYNCHRONOUSLY,
// so the deletion is authoritative the instant the DELETE handler returns —
// instead of waiting up to one tick (2s) for tick() to notice the row vanished
// from ListActive. It:
//
//   - cancels any in-flight init goroutine (pending) so it stops resolving
//     metadata immediately;
//   - drops the tracked entry and its retry counter;
//   - records a tombstone so a late initDownload (one that finished EnsureActive
//     /GotInfo just as the delete landed) does NOT re-promote the row or
//     re-register streamer protection — closing the resurrection window;
//   - drops the torrent from anacrolix (outside the lock) so it stops leeching,
//     mirroring the tick-driven cancel/pause path.
//
// infoHash is the row's hash (the handler already has it from the deleted row),
// used to drop the torrent even when nothing was tracked yet (delete of a
// queued/initializing row). A safe no-op when the worker never tracked the ID.
func (w *Worker) Remove(id int, infoHash string) {
	rm := w.removeState(id, infoHash)
	// Removing ONE file of a multi-file torrent: keep the torrent leeching the
	// rest and stop fetching this file — unless another row (any user) still
	// wants it, which only the union of rows can tell.
	if rm.siblingKeepsTorrent {
		w.reconcileFilePriorities(rm.hash, rm.files)
		return
	}

	if rm.haveHash {
		// Last member gone → drop the torrent. Drop runs OUTSIDE w.mu (streamer
		// lock + I/O) and is a safe no-op if a player still holds a viewer lease.
		// dropTorrentSeed (not the plain drop) because Remove is an explicit user
		// removal: the persisted auto-seed must go too, or resumeSeeding brings
		// the torrent back as a live "Seeding" card on the next boot.
		w.dropTorrentSeed(rm.hash)
	}
}

// removal is what Remove learned while tearing down a row's in-memory state.
type removal struct {
	hash     metainfo.Hash
	haveHash bool
	// files are the torrent files the row had marked wanted (tracked target
	// and/or an in-flight init's claim).
	files               []*torrent.File
	siblingKeepsTorrent bool
}

func (w *Worker) removeState(id int, infoHash string) removal {
	var rm removal

	// Resolve the hash up front: prefer the persisted infoHash (the handler
	// already has it from the deleted row) so we can detect siblings even when
	// THIS row was never tracked (a queued/initializing member).
	if infoHash != "" {
		if err := rm.hash.FromHexString(infoHash); err == nil {
			rm.haveHash = true
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.removed[id] = struct{}{}
	if cancel := w.pending[id]; cancel != nil {
		cancel()
		if f := w.pendingWant[id]; f != nil {
			rm.files = append(rm.files, f)
		}
		w.clearPendingLocked(id)
	}
	if removed := w.tracked[id]; removed != nil {
		if removed.hash != (metainfo.Hash{}) {
			rm.hash, rm.haveHash = removed.hash, true
		}
		if removed.file != nil {
			rm.files = append(rm.files, removed.file)
		}
		delete(w.tracked, id)
		w.unregisterLocked(removed) // drops streamer protection unless a sibling shares the name
	}
	delete(w.retries, id)
	// Aggregate-by-torrent: keep the torrent alive if ANY sibling file of the SAME
	// torrent still needs it — one already tracked OR one still resolving in a
	// shared init (pendingHash). Both checks run AFTER deleting our own entries so
	// we never count ourselves.
	rm.siblingKeepsTorrent = rm.haveHash &&
		(w.hashTrackedLocked(rm.hash) || w.pendingSiblingLocked(rm.hash, id))
	return rm
}

// hashTrackedLocked reports whether ANY tracked download still maps to hash —
// i.e. a sibling file of the same torrent is still being driven. Caller holds w.mu.
func (w *Worker) hashTrackedLocked(hash metainfo.Hash) bool {
	if hash == (metainfo.Hash{}) {
		return false
	}
	for _, td := range w.tracked {
		if td.hash == hash {
			return true
		}
	}
	return false
}

// dropTorrent drops a torrent via the injected seam (nil-safe).
func (w *Worker) dropTorrent(h metainfo.Hash) {
	if w.drop != nil {
		w.drop(h)
	}
}

// dropTorrentSeed drops a torrent AND its persisted auto-seed via the injected
// seam (nil-safe; falls back to the plain drop when only that is wired, e.g.
// in unit tests that override `drop` directly).
func (w *Worker) dropTorrentSeed(h metainfo.Hash) {
	if w.dropSeed != nil {
		_ = w.dropSeed(h)
		return
	}
	w.dropTorrent(h)
}
