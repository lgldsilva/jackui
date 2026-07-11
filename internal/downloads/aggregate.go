package downloads

// Aggregate-by-torrent model.
//
// JackUI persists ONE row per (user_id, info_hash, file_index): a single-file
// download, a selected file inside a multi-file torrent, or the whole-torrent
// sentinel. anacrolix, however, treats the torrent as ONE unit. Driving the
// download per-row (one EnsureActive / VerifyFile / progress-sample / stall-cycle
// per file) was O(N) in CPU/RAM and OOM'd on big season packs (~389 files).
//
// The fix keeps the per-file rows (they carry the file SELECTION + per-file
// progress) but makes the SCHEDULER and the WORKER operate on the GROUP derived
// at runtime from the rows sharing a (user_id, info_hash):
//
//   - the scheduler counts ONE slot per group (MaxActive/PerUserMax are torrents,
//     not files);
//   - the worker activates the torrent ONCE per group, marks only the selected
//     files wanted (file priorities), samples the live torrent ONCE per group,
//     and runs ONE completion move + ONE stall cycle for the whole torrent.
//
// No new table, no migration: a single-file download is just a group of one, and
// the whole-torrent sentinel (FileIndexWholeTorrent) is already an aggregate
// group of one covering N files.

import (
	"fmt"
	"log"
	"time"

	"github.com/anacrolix/torrent"
)

// grpKey is the runtime grouping key: all rows of one torrent owned by one user
// share it. id is only consulted for pre-metadata rows (info_hash == "") so a
// magnet-only enqueue stays an independent group of one until the hash resolves
// — collapsing several hashless rows under one key would wrongly fuse unrelated
// downloads. With a real hash, id is ignored and every file of the torrent maps
// to the same key.
func grpKey(userID, id int, infoHash string) string {
	if infoHash == "" {
		return fmt.Sprintf("%d:id:%d", userID, id)
	}
	return fmt.Sprintf("%d:%s", userID, infoHash)
}

// grpKeyOf derives the grouping key for a row.
func grpKeyOf(d Download) string {
	return grpKey(d.UserID, d.ID, d.InfoHash)
}

// Group is a set of download rows that belong to ONE torrent of ONE user — the
// unit the scheduler and worker reason about. Members preserves the input order
// so the expanded plan is deterministic.
type Group struct {
	Key     string
	UserID  int
	Members []Download
}

// GroupRows folds rows into per-(user, info_hash) groups, preserving first-seen
// order of both groups and members so callers (scheduler) stay deterministic.
// Pure and side-effect free — unit-testable in isolation.
func GroupRows(items []Download) []Group {
	order := make([]string, 0, len(items))
	byKey := make(map[string]*Group, len(items))
	for _, d := range items {
		k := grpKeyOf(d)
		g, ok := byKey[k]
		if !ok {
			g = &Group{Key: k, UserID: d.UserID}
			byKey[k] = g
			order = append(order, k)
		}
		g.Members = append(g.Members, d)
	}
	out := make([]Group, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out
}

// representative picks the row that drives the group's scheduling rank: the
// highest base priority, breaking ties toward the row promotionLess would
// promote first (so the group inherits its strongest member's standing). It also
// reports whether ANY member is already downloading — the group is then an
// incumbent and keeps its slot like a single downloading row would.
func (g Group) representative(st SchedSettings, now time.Time) (rep Download, downloading bool) {
	for i, m := range g.Members {
		if m.Status == StatusDownloading {
			downloading = true
		}
		if i == 0 {
			rep = m
			continue
		}
		if groupMemberStronger(m, rep, st, now) {
			rep = m
		}
	}
	return rep, downloading
}

// groupMemberStronger reports whether a should out-rank b as the group's
// representative. A higher base priority always wins; within the same priority
// the member promotionLess would order first wins (fewer stalls / older / lower
// id) so the representative is stable.
func groupMemberStronger(a, b Download, st SchedSettings, now time.Time) bool {
	if pa, pb := priorityBase(a.Priority), priorityBase(b.Priority); pa != pb {
		return pa > pb
	}
	return promotionLess(a, b, st, now)
}

// memberIDs returns the download IDs of every row in the group.
func (g Group) memberIDs() []int {
	ids := make([]int, 0, len(g.Members))
	for _, m := range g.Members {
		ids = append(ids, m.ID)
	}
	return ids
}

// isWhole reports whether the group is a whole-torrent download — ANY member
// carries the FileIndexWholeTorrent sentinel. A whole-torrent row's DownloadAll
// already fetches every file, so if the user ALSO has per-file rows of the same
// (user, info_hash) (the UNIQUE constraint allows it — distinct file_index), the
// group is a MIXED bag that must be driven as WHOLE: mixing DownloadAll with
// per-file Cancels would fight over piece priorities and resolveFileIndex(-2)
// would fail. The whole member is the sole driver (wholeMember); the redundant
// per-file rows ride along (they're covered by DownloadAll).
func (g Group) isWhole() bool {
	for _, m := range g.Members {
		if m.IsWholeTorrent() {
			return true
		}
	}
	return false
}

// wholeMember returns the FileIndexWholeTorrent row that drives a whole group (or
// the first member as a defensive fallback — callers only invoke this when
// isWhole() is true).
func (g Group) wholeMember() Download {
	for _, m := range g.Members {
		if m.IsWholeTorrent() {
			return m
		}
	}
	return g.Members[0]
}

// groupState is the live in-memory view of a torrent group, snapshotted under
// w.mu: which member rows are already tracked (and their trackedDL), whether an
// init is in flight, and the shared *torrent.Torrent (any tracked member's).
type groupState struct {
	tracked    map[int]*trackedDL // member id → trackedDL (only initialized members)
	hasTracked bool
	anyPending bool
	torrent    *torrent.Torrent // the group's live torrent (nil until a member inits)
}

// groupState snapshots the worker's in-memory tracking for one group's members.
// It also prunes tracked entries whose torrent is no longer active (mirrors the
// per-row reconcile's torrentStillActive guard) so a dropped torrent forces a
// re-init.
func (w *Worker) groupState(g Group) groupState {
	st := groupState{tracked: make(map[int]*trackedDL, len(g.Members))}
	w.mu.Lock()
	for _, m := range g.Members {
		if td := w.tracked[m.ID]; td != nil {
			st.tracked[m.ID] = td
			if st.torrent == nil {
				st.torrent = td.torrent
			}
		}
		if _, p := w.pending[m.ID]; p {
			st.anyPending = true
		}
	}
	w.mu.Unlock()
	// Drop tracked members whose torrent died (e.g. evicted) so the group re-inits.
	if st.torrent != nil && !w.torrentActive(st.torrent) {
		w.mu.Lock()
		for id := range st.tracked {
			delete(w.tracked, id)
		}
		w.mu.Unlock()
		return groupState{tracked: map[int]*trackedDL{}}
	}
	st.hasTracked = len(st.tracked) > 0
	return st
}

// torrentActive reports whether the streamer's client still has this torrent
// (nil-safe; mirrors torrentStillActive but takes the live torrent).
func (w *Worker) torrentActive(t *torrent.Torrent) bool {
	if t == nil {
		return false
	}
	c := w.streamer.Client()
	if c == nil {
		return false
	}
	_, ok := c.Torrent(t.InfoHash())
	return ok
}

// reconcileGroup brings one torrent's worth of rows in line with the running
// anacrolix client in ONE pass: it ensures the torrent is active exactly once,
// marks only the SELECTED files wanted (file priorities), samples the live
// torrent once and batches per-file progress, and fires completion once when
// every selected file is on disk. Whole-torrent and single-file are just the
// degenerate one-member cases.
func (w *Worker) reconcileGroup(g Group) {
	// A whole-torrent group (possibly MIXED with redundant per-file rows of the
	// same hash) is driven entirely by the FileIndexWholeTorrent row's single-row
	// path: DownloadAll fetches everything, so the per-file rows need no separate
	// init/priority handling. Reduce to the whole member and reuse reconcile.
	if g.isWhole() {
		w.reconcile(g.wholeMember())
		return
	}
	state := w.groupState(g)
	switch {
	case state.hasTracked:
		// The torrent is live: adopt any not-yet-tracked siblings onto it (cheap —
		// no re-EnsureActive / re-Verify), then sample + check completion as a unit.
		w.adoptSiblings(g, state)
		w.sampleGroup(g, state)
		w.checkGroupCompletion(g, state)
	case state.anyPending:
		return // an init for this group is already in flight; wait for it
	default:
		w.startGroupInit(g) // first time: ONE init for the whole torrent
	}
}

// sampleGroup samples the group's live torrent ONCE and persists every selected
// member's BytesCompleted in a single transaction (UpdateProgressBatch) — the
// I/O that O(N)-per-file sampling used to multiply. It also advances each
// member's stall clock (forward progress only) so detectStalls can reason per
// torrent.
func (w *Worker) sampleGroup(g Group, state groupState) {
	// Sample each file's BytesCompleted WITHOUT holding w.mu — progress() takes the
	// anacrolix client rLock, and holding w.mu across N of those on a 389-file pack
	// would serialize the whole worker (the contention the refactor kills). state's
	// trackedDL pointers were snapshotted under w.mu in groupState already.
	type sample struct {
		td        *trackedDL
		member    Download
		completed int64
	}
	samples := make([]sample, 0, len(g.Members))
	updates := make([]ProgressUpdate, 0, len(g.Members))
	for _, m := range g.Members {
		td := state.tracked[m.ID]
		if td == nil {
			continue
		}
		completed, _, ok := td.progress() // no w.mu held here
		if !ok {
			continue
		}
		if completed > m.BytesDownloaded {
			updates = append(updates, ProgressUpdate{UserID: m.UserID, ID: m.ID, Bytes: completed})
		}
		samples = append(samples, sample{td: td, member: m, completed: completed})
	}
	// Re-acquire w.mu only to advance the stall clock — a few cheap field writes,
	// no client locks underneath.
	now := time.Now()
	w.mu.Lock()
	for _, s := range samples {
		if s.td.lastProgressAt.IsZero() || s.completed > s.td.lastProgressBytes {
			s.td.lastProgressBytes = s.completed
			s.td.lastProgressAt = now
		}
	}
	w.mu.Unlock()
	if err := w.store.UpdateProgressBatch(updates); err != nil {
		log.Printf("downloads: batch progress for group %s: %v", g.Key, err)
	}
}
