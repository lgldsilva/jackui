package downloads

import (
	"log"
	"time"
)

// applySchedule enforces the active limit and priority order: it promotes queued
// rows into free slots and preempts a downloading row when a strictly
// higher-priority row is waiting (see schedulePlan, which counts ONE slot per
// torrent group). Promotion only flips the status; the next tick's reconcileGroup
// does the heavy init work — ONCE per torrent. Status transitions go through the
// batch store helpers so a multi-file pack flips in one transaction per group;
// the in-memory teardown of a preempted group is per member (preemptActive).
func (w *Worker) applySchedule(qs QueueSettings) {
	schedulable, err := w.store.ListSchedulable()
	if err != nil {
		log.Printf("downloads: list schedulable failed: %v", err)
		return
	}
	plan := schedulePlan(schedulable, qs.sched(), time.Now())
	for _, g := range GroupRows(schedulable) {
		w.applyGroupSchedule(g, plan)
	}
}

// applyGroupSchedule promotes or preempts a whole torrent group per the plan: a
// group chosen by the scheduler has every queued member promoted (batch tx); a
// group dropped from the plan has every downloading member preempted (batch DB
// transition + per-member in-memory teardown). A group with members on both
// sides can't happen — schedulePlan expands to ALL or NONE of a group's ids.
func (w *Worker) applyGroupSchedule(g Group, plan map[int]bool) {
	var promote, preempt []Download
	for _, m := range g.Members {
		switch {
		case plan[m.ID] && m.Status == StatusQueued:
			promote = append(promote, m)
		case !plan[m.ID] && m.Status == StatusDownloading:
			preempt = append(preempt, m)
		}
	}
	if ids := downloadIDs(promote); len(ids) > 0 {
		if got, _ := w.store.PromoteGroup(ids); len(got) > 0 {
			log.Printf("downloads: promoted torrent %q (%d row(s)) → downloading", g.Members[0].Name, len(got))
		}
	}
	if len(preempt) > 0 {
		w.preemptGroup(preempt)
	}
}

// preemptGroup demotes a whole torrent group back to the queue (over limit /
// out-prioritized) in one DB transaction, then tears down each member's
// in-memory tracking. No stall is counted. Delegates the per-member teardown to
// preemptActive's logic via preemptTeardown so the proven path stays shared.
func (w *Worker) preemptGroup(members []Download) {
	demoted, err := w.store.PreemptGroup(downloadIDs(members))
	if err != nil || len(demoted) == 0 {
		return
	}
	demotedSet := make(map[int]bool, len(demoted))
	for _, id := range demoted {
		demotedSet[id] = true
	}
	for _, m := range members {
		if demotedSet[m.ID] {
			w.preemptTeardown(m)
		}
	}
}

// downloadIDs extracts the IDs of a slice of downloads.
func downloadIDs(ds []Download) []int {
	ids := make([]int, 0, len(ds))
	for _, d := range ds {
		ids = append(ids, d.ID)
	}
	return ids
}

// preemptActive demotes a single downloading row back to the queue (over limit or
// out-prioritized by the scheduler) and tears down its in-memory tracking. No
// stall is counted — this isn't a no-seed stall. Retained for the single-row
// callers/tests; the tick's group path uses preemptGroup (batch DB) + the shared
// preemptTeardown.
func (w *Worker) preemptActive(d Download) {
	if ok, _ := w.store.PreemptToQueued(d.ID); !ok {
		return
	}
	w.preemptTeardown(d)
}

// preemptTeardown drops a preempted row's in-memory tracking (tracked entry,
// in-flight init, retry counter), releasing eviction protection unless a sibling
// still needs it. The DB transition is the caller's responsibility.
func (w *Worker) preemptTeardown(d Download) {
	w.mu.Lock()
	if td := w.tracked[d.ID]; td != nil {
		delete(w.tracked, d.ID)
		w.unregisterLocked(td)
	}
	if cancel := w.pending[d.ID]; cancel != nil {
		cancel()
		w.clearPendingLocked(d.ID)
	}
	delete(w.retries, d.ID)
	w.mu.Unlock()
	log.Printf("downloads: preempted #%d %q → queued (over limit / lower priority)", d.ID, d.Name)
}

// unregisterLocked drops the streamer's eviction protection for td's torrent,
// but only when no OTHER tracked download shares the same torrent name — the
// streamer keys protection by name (a set, not a refcount), so unregistering
// blindly would expose a sibling file of the same torrent to LRU eviction.
// Caller must hold w.mu.
func (w *Worker) unregisterLocked(td *trackedDL) {
	for id, other := range w.tracked {
		if id != td.id && other.name == td.name {
			return // a sibling still needs the protection
		}
	}
	w.streamer.UnregisterDownload(td.name)
}

// queueSettings returns the live settings, falling back to defaults when no
// getter is wired or it returns a zero MaxActive.
func (w *Worker) queueSettings() QueueSettings {
	qs := DefaultQueueSettings()
	if w.settings != nil {
		if got := w.settings(); got.MaxActive > 0 {
			qs = got
		}
	}
	return qs
}
