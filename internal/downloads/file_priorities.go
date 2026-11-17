package downloads

import (
	"context"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// File priority is ONE value per file of the shared anacrolix handle, while
// download rows are per user (and per file). Deriving it from the acting row
// let one row's init/remove/pause overrule every other row of the same
// torrent: user A's group init cancelled the file user B was downloading, and
// removing A's row stopped a file B's row still wanted. Priorities are
// therefore derived from the UNION of rows that want a file — tracked rows
// plus in-flight inits that already claimed theirs.
//
// Whole-torrent rows are deliberately absent from the union: DownloadAll
// raises PIECE priorities, and anacrolix takes the max of piece and file
// priority (Piece.purePriority), so lowering a file never stops a
// whole-torrent row.

// wantedPathsLocked returns the torrent-relative paths wanted by any row of
// hash. Paths (not *torrent.File pointers) so a row tracked against an older
// handle of the same torrent still counts. Caller holds w.mu.
func (w *Worker) wantedPathsLocked(hash metainfo.Hash) map[string]bool {
	out := make(map[string]bool)
	for _, td := range w.tracked {
		if td.hash == hash && td.file != nil {
			out[td.file.Path()] = true
		}
	}
	for id, f := range w.pendingWant {
		if w.pendingHash[id] == hash {
			out[f.Path()] = true
		}
	}
	return out
}

// reconcileFilePriorities brings the given files of hash in line with the
// union: wanted by some row → downloading, wanted by none → PiecePriorityNone.
// Callers pass only the files whose demand may have changed (the files a
// removed/paused row released, or every file after a group init), so files
// nobody asked about keep whatever priority they had.
func (w *Worker) reconcileFilePriorities(hash metainfo.Hash, files []*torrent.File) {
	if len(files) == 0 {
		return
	}
	// prioMu makes "read the union, then apply it" atomic against a concurrent
	// claim; without it a reconcile that read the union before another init's
	// claim could cancel that init's file right after it was marked wanted.
	w.prioMu.Lock()
	defer w.prioMu.Unlock()
	w.mu.Lock()
	wanted := w.wantedPathsLocked(hash)
	w.mu.Unlock()
	for _, f := range files {
		if f == nil {
			continue
		}
		if !wanted[f.Path()] {
			f.SetPriority(torrent.PiecePriorityNone)
			continue
		}
		// Raise only from None so a low/high label set on purpose survives.
		if f.Priority() == torrent.PiecePriorityNone {
			f.Download()
		}
	}
}

// claimAndWant marks f wanted on behalf of the init running under ctx and
// records the claim, so a reconcile triggered by ANOTHER row between here and
// the row's promotion into `tracked` keeps the file. The claim lives with the
// pending entry (clearPendingLocked drops it); once tracked, the trackedDL
// carries the demand.
func (w *Worker) claimAndWant(ctx context.Context, id int, hash metainfo.Hash, f *torrent.File) {
	w.prioMu.Lock()
	defer w.prioMu.Unlock()
	w.mu.Lock()
	if w.ownsPendingLocked(id, initGen(ctx)) {
		w.pendingWant[id] = f
		// A single-row init registers before the hash is known; fill it in so
		// the claim is attributable to its torrent.
		w.pendingHash[id] = hash
	}
	w.mu.Unlock()
	f.Download()
}

// releasedFiles collects, per torrent, the files whose rows stopped wanting
// them in one pass, so each torrent is reconciled once.
type releasedFiles map[metainfo.Hash][]*torrent.File

func (r releasedFiles) add(hash metainfo.Hash, f *torrent.File) {
	if f == nil || hash == (metainfo.Hash{}) {
		return
	}
	r[hash] = append(r[hash], f)
}

// reconcileReleased re-derives the priority of every released file.
func (w *Worker) reconcileReleased(r releasedFiles) {
	for hash, files := range r {
		w.reconcileFilePriorities(hash, files)
	}
}
