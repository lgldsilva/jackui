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
	"context"
	"fmt"
	"log"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/transfer"
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
	w.tracker.Submit(name, "download-move", len(movers), total, func(job *transfer.Job) {
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
