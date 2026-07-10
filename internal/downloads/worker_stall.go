package downloads

import (
	"log"
	"time"
)

// detectStalls demotes downloads that have made no progress for >= the stall
// threshold AND have zero connected seeders (a true no-seed stall, not just a
// slow download). The unit is the TORRENT: stalled victims are grouped by
// (user, info_hash) and the WHOLE group is demoted together (one stall cycle per
// torrent), so a multi-file pack doesn't thrash file-by-file. Demoting frees the
// slot and sends every member to the end of its priority group. After MaxStalls
// the whole group is paused (the user's choice: it stops cycling, not failed).
func (w *Worker) detectStalls(qs QueueSettings) {
	if qs.StallThresholdMin <= 0 {
		return
	}
	for _, victims := range w.groupStallVictims(qs) {
		// Phase 2: before demoting, try rotating to an alternative source. One
		// rotation per torrent (the representative member); on success the group
		// keeps its slot and re-inits with the new magnet.
		if qs.RotationEnabled && w.tryRotate(victims[0], qs) {
			continue
		}
		w.demoteStalledGroup(victims, qs)
	}
}

// groupStallVictims folds the no-seed stall victims into per-(user, info_hash)
// buckets so the whole torrent is demoted as a unit. A victim with no info_hash
// (pre-metadata) keys on its id, staying an independent group of one — matching
// the single-row behavior the existing detectStalls tests assert.
func (w *Worker) groupStallVictims(qs QueueSettings) [][]*trackedDL {
	order := make([]string, 0)
	byKey := make(map[string][]*trackedDL)
	for _, td := range w.collectStallVictims(qs) {
		k := grpKey(td.userID, td.id, td.infoHash)
		if _, ok := byKey[k]; !ok {
			order = append(order, k)
		}
		byKey[k] = append(byKey[k], td)
	}
	out := make([][]*trackedDL, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out
}

// demoteStalledGroup demotes EVERY member of a stalled torrent in one batch (one
// stall counted per member, so they cross MaxStalls together), tears down their
// tracking, and pauses the whole group once it has cycled MaxStalls times.
func (w *Worker) demoteStalledGroup(victims []*trackedDL, qs QueueSettings) {
	ids := make([]int, 0, len(victims))
	for _, td := range victims {
		ids = append(ids, td.id)
	}
	demoted, err := w.store.DemoteGroup(ids)
	if err != nil || len(demoted) == 0 {
		return
	}
	w.mu.Lock()
	for _, td := range victims {
		delete(w.tracked, td.id)
		delete(w.retries, td.id)
		w.unregisterLocked(td)
	}
	w.mu.Unlock()
	rep := victims[0]
	stalls := w.maxStallCount(demoted, rep.userID)
	log.Printf("downloads: torrent %q stalled (no seed for %dm) → %d row(s) requeued (stall #%d)",
		rep.name, qs.StallThresholdMin, len(demoted), stalls)
	if qs.MaxStalls > 0 && stalls >= qs.MaxStalls {
		if _, err := w.store.SetStatusByIDs(rep.userID, demoted, StatusPaused); err != nil {
			log.Printf("downloads: failed to pause stalled torrent %q: %v", rep.name, err)
		}
		log.Printf("downloads: torrent %q paused after %d no-seed stalls", rep.name, stalls)
	}
}

// maxStallCount returns the highest stall counter among the given rows (the
// group crosses MaxStalls when its most-stalled member does).
func (w *Worker) maxStallCount(ids []int, userID int) int {
	max := 0
	for _, id := range ids {
		if d, _ := w.store.Get(userID, id); d != nil && d.Stalls > max {
			max = d.Stalls
		}
	}
	return max
}

// collectStallVictims returns tracked downloads with no progress for >= the
// threshold AND zero connected seeders (a true no-seed stall, not just slow).
func (w *Worker) collectStallVictims(qs QueueSettings) []*trackedDL {
	threshold := time.Duration(qs.StallThresholdMin) * time.Minute
	now := time.Now()
	var victims []*trackedDL
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, td := range w.tracked {
		if td.lastProgressAt.IsZero() || now.Sub(td.lastProgressAt) < threshold {
			continue // never sampled, or progressed recently
		}
		if td.torrent != nil && td.torrent.Stats().ConnectedSeeders > 0 {
			continue // seeders present — not a no-seed stall
		}
		victims = append(victims, td)
	}
	return victims
}
