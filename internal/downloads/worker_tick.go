package downloads

import (
	"log"
	"time"

	"github.com/anacrolix/torrent/metainfo"
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
	// nothing ("fica lá baixando"). unregisterLocked above already cleared the
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
