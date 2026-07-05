package downloads

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/tmdb"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// sourceSearcher is the subset of the Jackett client the worker needs for source
// rotation (Phase 2). An interface keeps the worker testable without a live client.
type sourceSearcher interface {
	Search(query, category string, indexers []string) ([]jackett.Result, error)
}

// maxInitRetries caps how many times a slow/dead magnet is retried (in memory)
// before the download is marked failed. Each retry happens on a later tick, so
// transient swarm hiccups self-heal without the user re-queueing manually.
const maxInitRetries = 3

// logFmtSetFilePathFailed is the shared log format for a failed SetFilePath
// (used in the 3 completion-move paths), kept as a const to avoid duplication.
const logFmtSetFilePathFailed = "downloads: failed to set file path for download %d: %v"

// QueueSettings are the live scheduling knobs the worker reads each tick. A nil
// Settings getter (or zero values) falls back to DefaultQueueSettings.
type QueueSettings struct {
	MaxActive         int  // GLOBAL ceiling: concurrent downloads across all users (streaming excluded)
	PerUserMaxActive  int  // per-user concurrent cap; 0 = no per-user limit
	StallThresholdMin int  // minutes with no progress AND no seeders before a demote
	MaxStalls         int  // stalls before the download is paused (0 = never pause, cycle forever)
	AgingStepMin      int  // queue aging: minutes of waiting per +1 bonus (0 disables)
	AgingCap          int  // ceiling on the aging bonus
	RotationEnabled   bool // Phase 2: on a no-seed stall, try alternative sources before demoting
	AutoPromoteArr    bool // route completed *arr (RPC) downloads straight into SharedDir/<category>
}

// DefaultQueueSettings mirrors the config defaults; used when no getter is wired.
func DefaultQueueSettings() QueueSettings {
	return QueueSettings{MaxActive: 3, PerUserMaxActive: 0, StallThresholdMin: 30, MaxStalls: 3, AgingStepMin: 60, AgingCap: 150}
}

func (q QueueSettings) sched() SchedSettings {
	return SchedSettings{MaxActive: q.MaxActive, PerUserMax: q.PerUserMaxActive, AgingStepMin: q.AgingStepMin, AgingCap: q.AgingCap}
}

// WorkerConfig groups the dependencies and options for creating a Worker.
type WorkerConfig struct {
	Store           *Store
	Streamer        *streamer.Streamer
	DataDir         string // streamer DataDir — where anacrolix stores pieces
	DownloadDir     string // destination for completed files (empty = keep in DataDir)
	SharedDir       string // shared "completed downloads" tree for *arr auto-promote (empty = disabled)
	Interval        time.Duration
	NtfyBaseURL     string               // default https://ntfy.sh
	NtfyTopic       string               // global default topic; per-user override via store
	NtfyToken       string               // optional access token for protected topics (Authorization: Bearer)
	ResolveUsername func(int) string     // optional username resolver for per-user subdir
	FallbackUser    string               // per-user subdir to use when ResolveUsername fails (so a download NEVER lands at the bare mount root, where the UserSubpath migration would relocate it and create dup folders)
	Settings        func() QueueSettings // live queue settings; nil → DefaultQueueSettings
	Jackett         sourceSearcher       // Phase 2 source rotation; nil disables Jackett re-search
	AIClient        *ai.Client           // nil → no AI auto-rename on completion
	TMDBClient      *tmdb.Client         // enriches the AI rename; may be nil
	Tracker         *transfer.Tracker    // global move/copy progress; nil disables progress reporting
}

// Worker reconciles download rows in the store with the running anacrolix
// torrent client. It runs a single ticker; each tick it:
//
//  1. Loads every row in status='downloading'
//  2. Ensures the underlying torrent is loaded in the streamer
//  3. Marks the target file for full download (priority = Normal across all pieces)
//  4. Samples bytes_completed and persists progress
//  5. Flips to 'completed' once all bytes are on disk; moves file to downloadDir
//
// The worker is singleton — start it once at boot. It owns the per-download
// state in `tracked` so we can cancel readers and unregister streamer
// protection on user-initiated cancel.
type Worker struct {
	store       *Store
	streamer    *streamer.Streamer
	dataDir     string // streamer DataDir — where anacrolix stores pieces
	downloadDir string // destination for completed files (empty = keep in DataDir)
	sharedDir   string // shared "completed downloads" tree for *arr auto-promote (empty = disabled)
	interval    time.Duration

	mu      sync.Mutex
	tracked map[int]*trackedDL         // fully initialized, being sampled — by download.ID
	pending map[int]context.CancelFunc // init goroutine in flight — cancel on removal/stop
	// pendingHash maps an in-flight init's download.ID → its torrent hash, so
	// Remove can tell whether a SIBLING file of the same torrent is still being
	// resolved (and must keep the shared torrent alive) even before any member is
	// tracked. Populated alongside `pending` once the hash is known; cleared with
	// it. A zero hash (pre-metadata single-file init) means "hash unknown yet".
	pendingHash map[int]metainfo.Hash
	retries     map[int]int // transient init failures per download.ID
	// removed is a tombstone set of download IDs deleted via Remove(). An init
	// goroutine that was already resolving metadata when the delete landed must
	// NOT promote the row back into `tracked` (or re-register streamer
	// protection) — it checks this set before promoting. Entries are cleared
	// when the goroutine that observed the tombstone exits, so an ID reused by a
	// later Create starts clean.
	removed map[int]struct{}

	// drop drops a torrent from anacrolix. A field (defaulting to
	// streamer.Drop) instead of a direct call so the cancel/remove teardown is
	// unit-testable without a live torrent client.
	drop func(metainfo.Hash)

	stop   chan struct{}
	doneWG sync.WaitGroup

	// ntfy notification config
	ntfyBaseURL string // default https://ntfy.sh
	ntfyTopic   string // global default topic; per-user override via store
	ntfyToken   string // optional access token for protected topics (Authorization: Bearer)
	ntfyClient  *http.Client

	// resolveUsername returns the username for a given userID (for per-user subdir).
	// nil or returning "" disables per-user isolation (legacy flat dir).
	resolveUsername func(userID int) string

	// fallbackUser is the per-user subdir used when resolveUsername transiently
	// fails (e.g. auth DB busy at boot). Keeps a download out of the bare mount
	// root, where the UserSubpath migration would relocate it into <user>/ and,
	// colliding with the live download, spawn "name (1)", "name (2)" dup folders.
	fallbackUser string

	// settings returns the live queue settings (read each tick). nil → defaults.
	settings func() QueueSettings

	// jackett re-searches for alternative sources during rotation. nil disables it.
	jackett sourceSearcher

	// AI auto-rename on completion: when aiClient != nil, a completed download is
	// re-organized Plex-style (best-effort, async). tmdbClient enriches it.
	aiClient   *ai.Client
	tmdbClient *tmdb.Client

	// tracker records post-download move progress (X/Y files, bytes, rate, ETA)
	// for the global Transfers dock. nil → no reporting (Job methods are nil-safe).
	tracker *transfer.Tracker
	// moveBackoff is the base delay between post-download move retries. A field
	// (not a const) so tests can shrink it; defaults to 2s in NewWorker.
	moveBackoff time.Duration
}

// wholeTarget is the slice of *torrent.Torrent the worker needs for
// whole-torrent rows: marking everything wanted (DownloadAll) and aggregate
// progress. An interface (instead of using td.torrent directly) so init,
// progress and completion can be unit-tested with a fake — a real
// *torrent.Torrent can't be constructed without a live client.
type wholeTarget interface {
	BytesCompleted() int64
	Length() int64
	Files() []*torrent.File
	DownloadAll()
}

type trackedDL struct {
	id       int
	userID   int
	infoHash string
	hash     metainfo.Hash
	torrent  *torrent.Torrent
	// Exactly one of file/whole is set: file for single-file rows, whole for
	// FileIndexWholeTorrent rows (aggregate progress over the entire torrent).
	file              *torrent.File
	whole             wholeTarget
	name              string
	startedAt         time.Time
	lastProgressBytes int64     // bytes at the last forward sample (stall detection)
	lastProgressAt    time.Time // when bytes last advanced
}

// progress returns the completed/total byte counts for the row's target
// (single file or whole torrent). ok=false when there's no target yet.
func (td *trackedDL) progress() (completed, total int64, ok bool) {
	if td.whole != nil {
		return td.whole.BytesCompleted(), td.whole.Length(), true
	}
	if td.file != nil {
		return td.file.BytesCompleted(), td.file.Length(), true
	}
	return 0, 0, false
}

// NewWorker constructs a worker from a config struct. Interval defaults to 2
// seconds when zero or negative. NtfyBaseURL defaults to "https://ntfy.sh"
// when empty.
func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.Interval <= 0 {
		cfg.Interval = 2 * time.Second
	}
	if cfg.NtfyBaseURL == "" {
		cfg.NtfyBaseURL = "https://ntfy.sh"
	}
	w := &Worker{
		store:           cfg.Store,
		streamer:        cfg.Streamer,
		dataDir:         cfg.DataDir,
		downloadDir:     cfg.DownloadDir,
		sharedDir:       cfg.SharedDir,
		interval:        cfg.Interval,
		tracked:         make(map[int]*trackedDL),
		pending:         make(map[int]context.CancelFunc),
		pendingHash:     make(map[int]metainfo.Hash),
		retries:         make(map[int]int),
		removed:         make(map[int]struct{}),
		stop:            make(chan struct{}),
		ntfyBaseURL:     cfg.NtfyBaseURL,
		ntfyTopic:       cfg.NtfyTopic,
		ntfyToken:       cfg.NtfyToken,
		ntfyClient:      &http.Client{Timeout: 10 * time.Second},
		resolveUsername: cfg.ResolveUsername,
		fallbackUser:    cfg.FallbackUser,
		settings:        cfg.Settings,
		jackett:         cfg.Jackett,
		aiClient:        cfg.AIClient,
		tmdbClient:      cfg.TMDBClient,
		tracker:         cfg.Tracker,
		moveBackoff:     2 * time.Second,
	}
	if cfg.Streamer != nil {
		w.drop = cfg.Streamer.Drop
	}
	// Pre-register eviction protection + self-heal completed orphans (extracted
	// to keep NewWorker's cognitive complexity low).
	registerExistingDownloads(cfg)
	return w
}

// registerExistingDownloads pre-registers eviction protection for existing
// downloads. Completed rows are protected only in legacy mode (no downloadDir),
// where the file stays in DataDir; with a downloadDir they were already moved,
// so DataDir pieces can be LRU-freed — except orphans whose move was interrupted
// (isOrphanedCompletion), which are re-queued so the worker re-moves them.
func registerExistingDownloads(cfg WorkerConfig) {
	all, err := cfg.Store.ListAll()
	if err != nil {
		return
	}
	for _, d := range all {
		switch {
		case d.Status == StatusFailed || d.Name == "":
			continue
		case d.Status == StatusMoving:
			rescueInterruptedMove(cfg, d)
		case d.Status == StatusCompleted && cfg.DownloadDir != "":
			rescueOrphanCompletion(cfg, d)
		default:
			cfg.Streamer.RegisterDownload(d.Name)
		}
	}
}

// rescueOrphanCompletion re-queues a completed download whose moved file went
// missing while its cache source survives (a move interrupted by a restart), so
// the worker re-moves it. A healthy completed row (file present, or no cache
// source) is left alone. Extracted to keep registerExistingDownloads flat.
func rescueOrphanCompletion(cfg WorkerConfig, d Download) {
	if !isOrphanedCompletion(d, cfg.DataDir) {
		return
	}
	if err := cfg.Store.SetStatus(d.UserID, d.ID, StatusQueued); err != nil {
		log.Printf("downloads: failed to set status queued for existing download %d: %v", d.ID, err)
	}
	cfg.Streamer.RegisterDownload(d.Name)
	log.Printf("downloads: re-queued orphan #%d %q (file_path missing, source still in cache)", d.ID, d.Name)
}

// rescueInterruptedMove resumes a post-download move left in `moving` by a
// restart: it re-registers eviction protection (so the cache source survives
// until the re-move finishes) and flips the row back to `downloading`, so the
// next tick re-runs checkCompletion → re-dispatches the move. The move helpers
// are idempotent (already-moved files are skipped), so a partial move re-runs
// safely without re-downloading. This is the "retoma no restart" guarantee.
func rescueInterruptedMove(cfg WorkerConfig, d Download) {
	cfg.Streamer.RegisterDownload(d.Name)
	if err := cfg.Store.SetStatus(d.UserID, d.ID, StatusDownloading); err != nil {
		log.Printf("downloads: failed to rescue interrupted move #%d: %v", d.ID, err)
		return
	}
	log.Printf("downloads: resuming interrupted move #%d %q (was 'moving' → re-dispatching)", d.ID, d.Name)
}

// Start launches the worker loop in a goroutine. Idempotent on the caller side
// — call once. Returns immediately.
func (w *Worker) Start() {
	w.doneWG.Add(1)
	go w.run()
	go w.autoSeedCompleted()
}

// autoSeedCompleted re-activates, on boot, every COMPLETED download whose tracker
// is configured for continuous seeding (e.g. jackui). EnsureActive picks up
// the relocated storage (the file lives in bulk, outside the cache), so anacrolix
// verifies it in place and SEEDS — no re-download. Only `completed` rows qualify:
// a paused/failed/removed row was stopped by the user and is left alone. Bounded
// concurrency keeps the metadata/verify storm in check.
func (w *Worker) autoSeedCompleted() {
	all, err := w.store.ListAll()
	if err != nil {
		return
	}
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	started := 0
	for _, d := range all {
		if d.Status != StatusCompleted || d.InfoHash == "" {
			continue
		}
		var h metainfo.Hash
		if err := h.FromHexString(d.InfoHash); err != nil {
			continue
		}
		if !w.streamer.MatchesSeedTrackerCached(h) {
			continue
		}
		started++
		wg.Add(1)
		sem <- struct{}{}
		go func(d Download) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if _, err := w.streamer.EnsureActive(ctx, d.SeedSource()); err != nil {
				log.Printf("downloads: auto-seed #%d %q failed: %v", d.ID, d.Name, err)
			}
		}(d)
	}
	if started > 0 {
		log.Printf("downloads: auto-seeding %d completed torrent(s) for seed-trackers", started)
	}
	wg.Wait()
}

// Stop signals the worker to exit and blocks until the loop returns. Safe to
// call multiple times if you guard with a sync.Once at the call site —
// otherwise close-of-closed-channel will panic.
func (w *Worker) Stop() {
	close(w.stop)
	// Cancel any in-flight init goroutines so Stop doesn't block up to 90s
	// waiting on a slow EnsureActive/GotInfo.
	w.mu.Lock()
	for _, cancel := range w.pending {
		cancel()
	}
	w.mu.Unlock()
	w.doneWG.Wait()
}

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

// reconcile brings the in-memory torrent state in line with one DB row. Always
// safe to call repeatedly — no-ops if nothing has changed since last tick.
//
// First-time setup (resolving the magnet + waiting on metadata) can block for
// up to 90s on a slow swarm, so it runs in a goroutine (tracked via `pending`)
// instead of blocking the tick loop — otherwise one dead magnet would freeze
// progress for every other active download.
func (w *Worker) reconcile(d Download) {
	w.mu.Lock()
	td, exists := w.tracked[d.ID]
	_, isPending := w.pending[d.ID]
	w.mu.Unlock()

	if exists && !w.torrentStillActive(td) {
		w.mu.Lock()
		delete(w.tracked, d.ID)
		w.mu.Unlock()
		exists = false
	}
	if !exists && isPending {
		return
	}
	if !exists {
		w.startInit(d)
		return
	}
	w.sampleProgress(d, td)
	w.checkCompletion(d, td)
}

func (w *Worker) torrentStillActive(td *trackedDL) bool {
	c := w.streamer.Client()
	if c == nil {
		return false
	}
	_, ok := c.Torrent(td.hash)
	return ok
}

func (w *Worker) startInit(d Download) {
	ctx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	w.setPendingLocked(d.ID, d.InfoHash, cancel)
	w.mu.Unlock()
	w.doneWG.Add(1)
	go w.initDownload(ctx, d)
}

// setPendingLocked records an in-flight init under id: its cancel func and the
// torrent hash (parsed from the row's infoHash; zero when unknown). Caller holds
// w.mu. Keeps `pending` and `pendingHash` in lockstep so Remove can find a
// sibling still in init.
func (w *Worker) setPendingLocked(id int, infoHash string, cancel context.CancelFunc) {
	w.pending[id] = cancel
	var h metainfo.Hash
	if infoHash != "" {
		_ = h.FromHexString(infoHash)
	}
	w.pendingHash[id] = h
}

// clearPendingLocked drops an id's in-flight init bookkeeping (cancel + hash).
// Caller holds w.mu.
func (w *Worker) clearPendingLocked(id int) {
	delete(w.pending, id)
	delete(w.pendingHash, id)
}

// pendingSiblingLocked reports whether ANY in-flight init (other than excludeID)
// is resolving the same non-zero torrent hash — a sibling file of the same
// torrent still being set up, which must keep the shared torrent alive. Caller
// holds w.mu.
func (w *Worker) pendingSiblingLocked(hash metainfo.Hash, excludeID int) bool {
	if hash == (metainfo.Hash{}) {
		return false
	}
	for id, h := range w.pendingHash {
		if id != excludeID && h == hash {
			return true
		}
	}
	return false
}

func (w *Worker) sampleProgress(d Download, td *trackedDL) {
	completed, _, ok := td.progress()
	if !ok {
		return
	}
	if completed < d.BytesDownloaded {
		log.Printf("downloads: ignoring transient regression #%d %q completed %d → %d (keeping DB) — peers=%d",
			d.ID, td.name, d.BytesDownloaded, completed, peerCount(td.torrent))
	} else if completed != d.BytesDownloaded {
		if err := w.store.UpdateProgress(d.UserID, d.ID, completed); err != nil {
			log.Printf("downloads: failed to update progress for download %d: %v", d.ID, err)
		}
	}
	// Track forward progress for no-seed stall detection. The first sample seeds
	// the clock; only a real byte advance resets it, so a download stuck at the
	// same byte count with no seeders eventually trips detectStalls.
	w.mu.Lock()
	if td.lastProgressAt.IsZero() || completed > td.lastProgressBytes {
		td.lastProgressBytes = completed
		td.lastProgressAt = time.Now()
	}
	w.mu.Unlock()
}

// checkCompletion fires when every byte is on disk: it hands the download off to
// the post-download move WITHOUT blocking the tick loop. It captures the move
// plan (the torrent-relative paths — cheap, just reading metadata), flips the row
// to `moving`, removes it from the tracked set (so the tick neither re-dispatches
// it nor untracks-and-unregisters it mid-move — eviction protection stays until
// the move goroutine releases it), opens a transfer.Job for the Transfers dock,
// and runs the actual relocation in its own goroutine. A nil/zero target (no
// file yet) is a no-op. This is the fix for "100% mas não finaliza": the slow
// cross-filesystem copy no longer wedges the tick, and a move that keeps failing
// ends as `failed` (with the error) instead of retrying silently forever.
func (w *Worker) checkCompletion(d Download, td *trackedDL) {
	completed, total, ok := td.progress()
	if !ok || total <= 0 || completed < total {
		return
	}
	whole := td.whole != nil
	var relPaths []string
	if whole {
		relPaths = wholeTorrentRelPaths(td.whole.Files())
	} else {
		relPaths = []string{td.file.Path()}
	}
	name := td.name

	// Enter the non-blocking "moving" phase. Order matters: flip status first (so
	// the next ListActive excludes the row — no double-dispatch), then drop the
	// tracked entry WITHOUT unregisterLocked (keep eviction protection alive for
	// the copy; the move goroutine calls UnregisterDownload when it lands).
	if err := w.store.SetStatus(d.UserID, d.ID, StatusMoving); err != nil {
		log.Printf("downloads: failed to set status moving #%d: %v", d.ID, err)
		return // stays downloading; the next tick retries the hand-off
	}
	w.mu.Lock()
	delete(w.tracked, d.ID)
	w.mu.Unlock()

	// Submit to the bounded transfer pool: the move waits FIFO for a slot (status
	// 'queued' in the dock) so many simultaneous completions don't thrash the disk
	// all at once. The download row stays 'moving' meanwhile (boot rescue covers a
	// restart while queued).
	w.tracker.Submit(name, "download-move", len(relPaths), total, func(job *transfer.Job) {
		w.runCompletionMove(d, name, relPaths, whole, total, job)
	})
}

// failOrRetry records a transient init failure. Below maxInitRetries it leaves
// the row in `downloading` so the next tick re-launches init; at the cap it
// flips the row to `failed`. A cancelled download (no longer in `pending`) is
// left untouched.
func (w *Worker) failOrRetry(d Download, msg string) {
	w.mu.Lock()
	_, stillPending := w.pending[d.ID]
	if !stillPending {
		w.mu.Unlock()
		return // cancelled during init — don't clobber status
	}
	n := w.retries[d.ID] + 1
	w.retries[d.ID] = n
	w.mu.Unlock()

	if n >= maxInitRetries {
		w.mu.Lock()
		delete(w.retries, d.ID)
		w.mu.Unlock()
		log.Printf("downloads: init #%d (%s) failed after %d tries: %s", d.ID, d.InfoHash, n, msg)
		if err := w.store.SetError(d.UserID, d.ID, msg); err != nil {
			log.Printf("downloads: failed to set error for download %d: %v", d.ID, err)
		}
		name := d.Name
		if name == "" {
			name = d.InfoHash
		}
		go w.sendNtfy(context.Background(), "Download falhou: "+name, msg, "x,torrent")
		return
	}
	log.Printf("downloads: init #%d (%s) transient failure %d/%d: %s", d.ID, d.InfoHash, n, maxInitRetries, msg)
	// Leave status=downloading — next tick re-launches initDownload.
}

// sendNtfy posts a push notification to ntfy.sh (or a self-hosted instance)
// for a download event. Uses the global default topic. Silently logs and drops
// errors after configured retries — notification delivery is best-effort.
func (w *Worker) sendNtfy(ctx context.Context, title, body, tags string) {
	if w.ntfyTopic == "" {
		return
	}
	backoff := []time.Duration{30 * time.Second, 2 * time.Minute, 5 * time.Minute}
	for i := 0; i <= len(backoff); i++ {
		url := fmt.Sprintf("%s/%s", strings.TrimRight(w.ntfyBaseURL, "/"), w.ntfyTopic)
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(body))
		if err != nil {
			log.Printf("downloads: ntfy request err: %v", err)
			return
		}
		req.Header.Set("Title", title)
		req.Header.Set("Tags", tags)
		if w.ntfyToken != "" {
			req.Header.Set("Authorization", "Bearer "+w.ntfyToken)
		}
		resp, err := w.ntfyClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return
			}
			err = fmt.Errorf("ntfy returned %d", resp.StatusCode)
		}
		if i < len(backoff) {
			log.Printf("downloads: ntfy notify failed (attempt %d/%d): %v — retrying in %v", i+1, len(backoff)+1, err, backoff[i])
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff[i]):
			}
		} else {
			log.Printf("downloads: ntfy notify failed after %d attempts: %v", len(backoff)+1, err)
		}
	}
}

// SnapshotActiveCount is mostly diagnostic — returns the number of downloads
// currently being driven by the worker (matches store.ListActive() after the
// next tick).
func (w *Worker) SnapshotActiveCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.tracked)
}

// pickBestFile selects the best file to download from a torrent file list.
// It prefers the largest video/media file (by extension), falling back to
// the largest file overall. Returns -1 if the list is empty.
func pickBestFile(files []*torrent.File) int {
	if len(files) == 0 {
		return -1
	}
	videoExt := map[string]bool{
		".mkv": true, ".mp4": true, ".avi": true, ".mov": true,
		".wmv": true, ".flv": true, ".webm": true, ".m4v": true,
		".ts": true, ".m2ts": true,
	}
	audioExt := map[string]bool{
		".mp3": true, ".flac": true, ".wav": true, ".m4a": true,
		".aac": true, ".ogg": true, ".opus": true,
	}

	bestIdx := 0
	bestScore := int64(-1)

	for i, f := range files {
		p := strings.ToLower(f.Path())
		score := f.Length()

		// Video files get a massive boost so they always win.
		for ext := range videoExt {
			if strings.HasSuffix(p, ext) {
				score += 1 << 40 // 1TB boost — video trumps everything
				break
			}
		}
		// Audio files get a moderate boost.
		for ext := range audioExt {
			if strings.HasSuffix(p, ext) {
				score += 1 << 30 // 1GB boost — audio over generic data
				break
			}
		}

		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	return bestIdx
}
