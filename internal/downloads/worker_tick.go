package downloads

import (
	"log"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// Bounded periodic sweep for rows stuck in `moving` (see sweepStuckMoving):
const (
	// movingSweepTicks is the sweep cadence in ticks (default 2s interval →
	// roughly once a minute).
	movingSweepTicks = 30
	// movingStuckAfter is how long a row may sit in `moving` without an active
	// in-process move before the sweep re-dispatches it.
	movingStuckAfter = 10 * time.Minute
)

func (w *Worker) run() {
	defer w.doneWG.Done()

	// Bootstrap: on startup, every row in status='downloading' should resume.
	// The tick handler does the actual reconciliation — we just kick it once
	// immediately so the user doesn't wait `interval` for resumes after a
	// restart.
	w.tick()

	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
			w.tick()
		}
	}
}

func (w *Worker) tick() {
	active, err := w.store.ListActive()
	if err != nil {
		log.Printf("downloads: list active failed: %v", err)
		return
	}

	// Build a set of currently-active IDs to detect removals (cancel/pause).
	wantIDs := make(map[int]bool, len(active))
	for _, d := range active {
		wantIDs[d.ID] = true
	}

	// Untrack any IDs that vanished from the active set since last tick
	// (user paused/cancelled, or a prior tick demoted them). Cancel in-flight
	// inits too so a cancelled download stops resolving metadata immediately. A
	// torrent is dropped ONLY when NO active row still shares its hash — a sibling
	// file of the same torrent (aggregate-by-torrent) must keep it leeching.
	stillWantedHashes := w.hashesStillWanted(active)
	w.mu.Lock()
	var toDrop []metainfo.Hash
	for id, td := range w.tracked {
		if !wantIDs[id] {
			w.unregisterLocked(td)
			delete(w.tracked, id)
			delete(w.retries, id)
			if td.hash != (metainfo.Hash{}) && !stillWantedHashes[td.hash] {
				toDrop = append(toDrop, td.hash)
			}
		}
	}
	for id, cancel := range w.pending {
		if !wantIDs[id] {
			cancel()
			w.clearPendingLocked(id)
			delete(w.retries, id)
		}
	}
	w.mu.Unlock()

	// Stop the torrent in anacrolix too. Pause/cancel/delete only flip the DB
	// status; without an explicit Drop the torrent kept leeching in the
	// background until the streamer's idle reaper — so "Pause" looked like it did
	// nothing ("it kept downloading"). unregisterLocked above already cleared the
	// download protection, so Drop won't be blocked by the protected guard. Drop
	// runs OUTSIDE w.mu (it takes the streamer lock + does I/O) and is a safe
	// no-op if a player still holds a viewer lease on the same torrent.
	for _, h := range toDrop {
		w.dropTorrent(h)
	}

	// Aggregate-by-torrent: drive each torrent ONCE per tick (one init/sample/
	// completion per group) instead of once per file, regardless of how many
	// files the torrent has selected.
	for _, g := range GroupRows(active) {
		w.reconcileGroup(g)
	}

	qs := w.queueSettings()
	w.detectStalls(qs)
	w.applySchedule(qs)

	w.maybeSweepStuckMoving()
}

// maybeSweepStuckMoving runs the stuck-moving sweep every movingSweepTicks
// ticks (bounded periodic — not per tick).
func (w *Worker) maybeSweepStuckMoving() {
	w.mu.Lock()
	w.ticks++
	due := w.ticks%movingSweepTicks == 0
	w.mu.Unlock()
	if due {
		w.sweepStuckMoving()
	}
}

// sweepStuckMoving re-dispatches rows wedged in `moving`: checkCompletion flips
// the status and only then submits the transfer job, so a crash in between (or
// a boot rescue that missed) leaves the row invisible — ListActive only returns
// `downloading`, so no tick would ever look at it again. A row is stuck when
// THIS process owns no move for it (not in movingActive) and either the
// dispatch was never recorded or it predates movingStuckAfter. Re-dispatch
// reuses the boot rescue verbatim: re-register eviction protection, flip the
// row back to `downloading`, let the next tick re-run the idempotent move.
func (w *Worker) sweepStuckMoving() {
	rows, err := w.store.ListMoving()
	if err != nil {
		log.Printf("downloads: stuck-moving sweep list failed: %v", err)
		return
	}
	now := time.Now()
	live := make(map[int]struct{}, len(rows))
	var stuck []Download
	w.mu.Lock()
	for _, d := range rows {
		live[d.ID] = struct{}{}
		if _, active := w.movingActive[d.ID]; active {
			continue // a move goroutine/queued job owns this row — a long copy is healthy
		}
		if started, seen := w.movingSince[d.ID]; seen && now.Sub(started) < movingStuckAfter {
			continue // recorded dispatch within the grace period — give the pool its turn
		}
		stuck = append(stuck, d)
	}
	// Prune bookkeeping for rows that left `moving` (completed/failed/paused).
	for id := range w.movingActive {
		if _, ok := live[id]; !ok {
			delete(w.movingActive, id)
			delete(w.movingSince, id)
		}
	}
	w.mu.Unlock()

	for _, d := range stuck {
		log.Printf("downloads: sweep re-dispatching stuck move #%d %q (no active move)", d.ID, d.Name)
		w.clearMovingState(d.ID)
		// Same rescue the boot path uses — factored, not duplicated.
		rescueInterruptedMove(WorkerConfig{Store: w.store, Streamer: w.streamer}, d)
	}
}

// markMovingDispatch records that THIS process dispatched a completion move for
// id (called by checkCompletion before the transfer job is submitted).
func (w *Worker) markMovingDispatch(id int) {
	w.mu.Lock()
	w.movingActive[id] = struct{}{}
	w.movingSince[id] = time.Now()
	w.mu.Unlock()
}

// clearMovingState drops the dispatch bookkeeping once the move goroutine
// returns (any outcome) — called via defer from runCompletionMove.
func (w *Worker) clearMovingState(id int) {
	w.mu.Lock()
	delete(w.movingActive, id)
	delete(w.movingSince, id)
	w.mu.Unlock()
}

// hashesStillWanted is the set of info hashes that ANY currently-active row maps
// to — used so the tick's untrack-vanished pass doesn't Drop a torrent a sibling
// file still depends on.
func (w *Worker) hashesStillWanted(active []Download) map[metainfo.Hash]bool {
	out := make(map[metainfo.Hash]bool, len(active))
	for _, d := range active {
		if d.InfoHash == "" {
			continue
		}
		var h metainfo.Hash
		if h.FromHexString(d.InfoHash) == nil {
			out[h] = true
		}
	}
	return out
}
