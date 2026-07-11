package downloads

import (
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
	var hash metainfo.Hash
	haveHash := false

	// Resolve the hash up front: prefer the persisted infoHash (the handler
	// already has it from the deleted row) so we can detect siblings even when
	// THIS row was never tracked (a queued/initializing member).
	if infoHash != "" {
		if err := hash.FromHexString(infoHash); err == nil {
			haveHash = true
		}
	}

	w.mu.Lock()
	w.removed[id] = struct{}{}
	if cancel := w.pending[id]; cancel != nil {
		cancel()
		w.clearPendingLocked(id)
	}
	removed := w.tracked[id]
	if removed != nil {
		if removed.hash != (metainfo.Hash{}) {
			hash, haveHash = removed.hash, true
		}
		delete(w.tracked, id)
		w.unregisterLocked(removed) // drops streamer protection unless a sibling shares the name
	}
	delete(w.retries, id)
	// Aggregate-by-torrent: keep the torrent alive if ANY sibling file of the SAME
	// torrent still needs it — one already tracked OR one still resolving in a
	// shared init (pendingHash). Both checks run AFTER deleting our own entries so
	// we never count ourselves.
	siblingKeepsTorrent := haveHash &&
		(w.hashTrackedLocked(hash) || w.pendingSiblingLocked(hash, id))
	w.mu.Unlock()

	// Removing ONE file of a multi-file torrent: just stop fetching that file
	// (PiecePriorityNone) and keep the torrent leeching the rest. A sibling still
	// in init has no live *torrent.File for us yet, but initGroup will reconcile
	// priorities; cancel ours if we have it.
	if siblingKeepsTorrent {
		if removed != nil && removed.file != nil {
			removed.file.Cancel()
		}
		return
	}

	if haveHash {
		// Last member gone → drop the torrent. Drop runs OUTSIDE w.mu (streamer
		// lock + I/O) and is a safe no-op if a player still holds a viewer lease.
		w.dropTorrent(hash)
	}
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
