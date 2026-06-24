package downloads

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/renamer"
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
	retries map[int]int                // transient init failures per download.ID
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
		retries:         make(map[int]int),
		removed:         make(map[int]struct{}),
		stop:            make(chan struct{}),
		ntfyBaseURL:     cfg.NtfyBaseURL,
		ntfyTopic:       cfg.NtfyTopic,
		ntfyToken:       cfg.NtfyToken,
		ntfyClient:      &http.Client{Timeout: 10 * time.Second},
		resolveUsername: cfg.ResolveUsername,
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
// is configured for continuous seeding (e.g. amigos-share). EnsureActive picks up
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
			if _, err := w.streamer.EnsureActive(ctx, d.EffectiveMagnet()); err != nil {
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

	w.mu.Lock()
	w.removed[id] = struct{}{}
	if cancel := w.pending[id]; cancel != nil {
		cancel()
		delete(w.pending, id)
	}
	if td := w.tracked[id]; td != nil {
		w.unregisterLocked(td) // drops streamer protection unless a sibling shares the name
		if td.hash != (metainfo.Hash{}) {
			hash, haveHash = td.hash, true
		}
		delete(w.tracked, id)
	}
	delete(w.retries, id)
	w.mu.Unlock()

	// Fall back to the row's persisted infoHash when nothing was tracked (a
	// queued row deleted before init ever ran has no trackedDL but may still
	// have an active torrent in the streamer).
	if !haveHash && infoHash != "" {
		if err := hash.FromHexString(infoHash); err == nil {
			haveHash = true
		}
	}
	if haveHash {
		// Drop runs OUTSIDE w.mu (streamer lock + I/O) and is a safe no-op if a
		// player still holds a viewer lease on the same torrent.
		w.dropTorrent(hash)
	}
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
	// inits too so a cancelled download stops resolving metadata immediately.
	w.mu.Lock()
	var toDrop []metainfo.Hash
	for id, td := range w.tracked {
		if !wantIDs[id] {
			w.unregisterLocked(td)
			delete(w.tracked, id)
			delete(w.retries, id)
			if td.hash != (metainfo.Hash{}) {
				toDrop = append(toDrop, td.hash)
			}
		}
	}
	for id, cancel := range w.pending {
		if !wantIDs[id] {
			cancel()
			delete(w.pending, id)
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

	for _, d := range active {
		w.reconcile(d)
	}

	qs := w.queueSettings()
	w.detectStalls(qs)
	w.applySchedule(qs)
}

// detectStalls demotes downloads that have made no progress for >= the stall
// threshold AND have zero connected seeders (a true no-seed stall, not just a
// slow download). Demoting frees the slot and sends the row to the end of its
// priority group. After MaxStalls demotes the download is paused (the user's
// choice: it stops cycling but isn't marked failed).
func (w *Worker) detectStalls(qs QueueSettings) {
	if qs.StallThresholdMin <= 0 {
		return
	}
	for _, td := range w.collectStallVictims(qs) {
		// Phase 2: before demoting, try rotating to an alternative source. If it
		// rotates, the download keeps its slot and re-inits with the new magnet.
		if qs.RotationEnabled && w.tryRotate(td, qs) {
			continue
		}
		w.demoteStalled(td, qs)
	}
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

// demoteStalled sends a stalled download to the back of its priority group and,
// once it has cycled MaxStalls times, pauses it (the user's choice: stop cycling
// without marking it failed).
func (w *Worker) demoteStalled(td *trackedDL, qs QueueSettings) {
	stalls, demoted, err := w.store.DemoteToQueued(td.id)
	if err != nil || !demoted {
		return
	}
	w.mu.Lock()
	delete(w.tracked, td.id)
	delete(w.retries, td.id)
	w.unregisterLocked(td)
	w.mu.Unlock()
	log.Printf("downloads: #%d %q stalled (no seed for %dm) → requeued (stall #%d)", td.id, td.name, qs.StallThresholdMin, stalls)
	if qs.MaxStalls > 0 && stalls >= qs.MaxStalls {
		if err := w.store.SetStatus(td.userID, td.id, StatusPaused); err != nil {
			log.Printf("downloads: failed to set status paused for stalled download %d: %v", td.id, err)
		}
		log.Printf("downloads: #%d %q paused after %d no-seed stalls", td.id, td.name, stalls)
	}
}

// applySchedule enforces the active limit and priority order: it promotes queued
// rows into free slots and preempts a downloading row when a strictly
// higher-priority row is waiting (see schedulePlan). Promotion only flips the
// status; the next tick's reconcile does the heavy init work.
func (w *Worker) applySchedule(qs QueueSettings) {
	schedulable, err := w.store.ListSchedulable()
	if err != nil {
		log.Printf("downloads: list schedulable failed: %v", err)
		return
	}
	plan := schedulePlan(schedulable, qs.sched(), time.Now())
	for _, d := range schedulable {
		switch {
		case plan[d.ID] && d.Status == StatusQueued:
			if ok, _ := w.store.PromoteToDownloading(d.ID); ok {
				log.Printf("downloads: promoted #%d %q (%s) → downloading", d.ID, d.Name, d.Priority)
			}
		case !plan[d.ID] && d.Status == StatusDownloading:
			w.preemptActive(d)
		}
	}
}

// preemptActive demotes a downloading row back to the queue (over limit or
// out-prioritized by the scheduler) and tears down its in-memory tracking. No
// stall is counted — this isn't a no-seed stall.
func (w *Worker) preemptActive(d Download) {
	if ok, _ := w.store.PreemptToQueued(d.ID); !ok {
		return
	}
	w.mu.Lock()
	if td := w.tracked[d.ID]; td != nil {
		delete(w.tracked, d.ID)
		w.unregisterLocked(td)
	}
	if cancel := w.pending[d.ID]; cancel != nil {
		cancel()
		delete(w.pending, d.ID)
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
	w.pending[d.ID] = cancel
	w.mu.Unlock()
	w.doneWG.Add(1)
	go w.initDownload(ctx, d)
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

// moveMaxAttempts bounds in-process retries of a post-download move before the
// row is marked `failed`. Without a bound a recurring error (e.g. the
// `permission denied` we saw on the destination) would retry forever, leaving the
// download wedged at "100% downloading". A move interrupted by app shutdown is
// NOT a failure: the row stays `moving` and boot rescue re-dispatches it.
const moveMaxAttempts = 3

// runCompletionMove performs the post-download relocation OFF the tick loop and
// finalizes the row. The move helpers are idempotent, so each retry resumes where
// the last left off. On success → `completed` (+ AI rename + ntfy); after
// moveMaxAttempts of a persistent error → `failed` with the message; on app
// shutdown mid-retry it returns leaving the row `moving` for boot rescue.
func (w *Worker) runCompletionMove(d Download, name string, relPaths []string, whole bool, total int64, job *transfer.Job) {
	dst, err := w.attemptCompletionMove(d, name, relPaths, whole, job)
	if err != nil {
		if e := w.store.SetError(d.UserID, d.ID, "move failed: "+err.Error()); e != nil {
			log.Printf("downloads: failed to mark move-failed #%d: %v", d.ID, e)
		}
		job.Fail(err)
		log.Printf("downloads: completion move #%d %q failed after %d attempts: %v", d.ID, name, moveMaxAttempts, err)
		return
	}
	if err := w.store.SetStatus(d.UserID, d.ID, StatusCompleted); err != nil {
		log.Printf("downloads: failed to set status completed for download %d: %v", d.ID, err)
	}
	job.Done()
	log.Printf("downloads: completed #%d %q", d.ID, name)
	// Seed-tracker content keeps seeding from its NEW (bulk) home instead of going
	// idle: the download torrent still points at the now-moved cache file, so we
	// swap it onto the relocated storage. Status is `completed` + file_path=bulk by
	// now, so EnsureActive's relocatedStorage resolves to the real file.
	go w.reseedAfterCompletion(d)
	// AI auto-rename (Plex-style) when configured. Whole-torrent rows skip it:
	// the rename chain targets ONE media file, not a tree of N files.
	if w.aiClient != nil && dst != "" && !whole {
		go w.aiRenameCompleted(d, dst)
	}
	body := fmt.Sprintf("%s · %.2f MB", name, float64(total)/1048576)
	go w.sendNtfy(context.Background(), "Download concluído: "+name, body, "white_check_mark,torrent")
}

// reseedAfterCompletion re-activates a just-completed download from its new bulk
// location when its tracker is configured for continuous seeding. The torrent
// that drove the download still has cache-rooted storage pointing at the file we
// just moved away, so Drop + EnsureActive swaps it onto the relocated storage
// (anacrolix verifies the bulk file and seeds — no re-download). No-op when the
// tracker isn't a seed-tracker.
func (w *Worker) reseedAfterCompletion(d Download) {
	if d.InfoHash == "" {
		return
	}
	var h metainfo.Hash
	if err := h.FromHexString(d.InfoHash); err != nil {
		return
	}
	if !w.streamer.MatchesSeedTrackerCached(h) {
		return
	}
	w.streamer.Drop(h)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := w.streamer.EnsureActive(ctx, d.EffectiveMagnet()); err != nil {
		log.Printf("downloads: reseed #%d %q failed: %v", d.ID, d.Name, err)
		return
	}
	log.Printf("downloads: #%d %q reseeding from bulk (seed-tracker)", d.ID, d.Name)
}

// attemptCompletionMove runs the move with bounded retries, reporting progress to
// job. It returns the destination path on success, or the last error after
// moveMaxAttempts. A shutdown signal (w.stop) aborts the retry loop early with
// the pending error so the caller leaves the row `moving` for boot rescue.
func (w *Worker) attemptCompletionMove(d Download, name string, relPaths []string, whole bool, job *transfer.Job) (string, error) {
	// Download-to-bulk: when the data was written STRAIGHT to its final
	// destination, there's nothing to move — finalize in place. Covers the
	// selected-file-in-multi case too (the storage preserves the internal tree,
	// which moveDownloadedFile would flatten). Falls through to the move when the
	// data ISN'T at the bulk path (no destination configured, or a legacy cache
	// download from before this change).
	if dst, ok := w.tryFinalizeBulk(d, name, relPaths, whole); ok {
		return dst, nil
	}
	var dst string
	var err error
	for attempt := 1; attempt <= moveMaxAttempts; attempt++ {
		if whole {
			dst, err = w.moveCompletedTorrentFiles(d, name, relPaths, job)
		} else {
			dst, err = w.moveCompletedFile(d, relPaths[0], name, job)
		}
		if err == nil {
			return dst, nil
		}
		log.Printf("downloads: completion move #%d %q attempt %d/%d: %v", d.ID, name, attempt, moveMaxAttempts, err)
		if attempt == moveMaxAttempts {
			break
		}
		select {
		case <-w.stop:
			return "", err // shutting down: leave the row `moving` for boot rescue
		case <-time.After(time.Duration(attempt) * w.moveBackoff):
		}
	}
	return "", err
}

// tryFinalizeBulk finalizes a download whose data was written DIRECTLY to its
// bulk destination (download-to-bulk): no move, just persist file_path and
// release the eviction guard. Returns ok=false (so the caller falls back to the
// cache→dest move) when no destination is configured OR the data isn't actually
// at the expected bulk path — e.g. a legacy download that landed in the cache
// before this change. dst is the file (or torrent dir) path on ok.
func (w *Worker) tryFinalizeBulk(d Download, name string, relPaths []string, whole bool) (string, bool) {
	if w.completionBaseDir(d) == "" {
		return "", false
	}
	destDir := w.completionDest(d, name)
	dst := destDir
	if whole {
		if !dirHasFiles(destDir) {
			return "", false
		}
	} else {
		dst = filepath.Join(destDir, bulkRelPath(name, relPaths[0]))
		if !fileExists(dst) {
			return "", false
		}
	}
	if err := w.store.SetFilePath(d.UserID, d.ID, dst); err != nil {
		log.Printf("downloads: failed to set file path for download %d: %v", d.ID, err)
	}
	w.streamer.UnregisterDownload(name)
	log.Printf("downloads: #%d %q already in bulk (no move) → %s", d.ID, name, dst)
	return dst, true
}

// bulkRelPath mirrors bulkRel for a torrent-relative path string: strips the
// torrent-name root so it matches the download storage layout.
func bulkRelPath(name, rel string) string {
	return filepath.FromSlash(strings.TrimPrefix(rel, name+"/"))
}

// wholeTorrentRelPaths returns the content files' torrent-relative paths, skipping
// BEP 47 pad files (attr "p") — piece-alignment filler, never materialized.
func wholeTorrentRelPaths(files []*torrent.File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if strings.Contains(f.FileInfo().Attr, "p") {
			continue
		}
		out = append(out, f.Path())
	}
	return out
}

// peerCount is nil-safe len(t.PeerConns()) for diagnostic logs (tests build
// trackedDLs without a live torrent).
func peerCount(t *torrent.Torrent) int {
	if t == nil {
		return 0
	}
	return len(t.PeerConns())
}

// partSuffix is what the anacrolix file storage appends to a file until all its
// pieces verify; a restart mid-verification can leave a *complete* .part behind.
const partSuffix = ".part"

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// resolveCompletedSrc locates the finished file in dataDir for a torrent-relative
// path: the final name, or the leftover ".part" the storage hasn't renamed yet.
// Returns "" when neither exists.
func resolveCompletedSrc(dataDir, relPath string) string {
	src := filepath.Join(dataDir, relPath)
	if fileExists(src) {
		return src
	}
	if part := src + partSuffix; fileExists(part) {
		return part
	}
	return ""
}

// completedDestDir builds the per-user, per-torrent destination directory.
func completedDestDir(downloadDir, username, torrentName string) string {
	dir := downloadDir
	if username != "" {
		dir = filepath.Join(dir, username)
	}
	return filepath.Join(dir, sanitizeFolderName(torrentName))
}

// PromoteDir returns the Transmission-style "completed downloads" directory for
// an *arr download: sharedDir/<sanitized category> (or just sharedDir when the
// category is empty). Shared by the worker (where the finished files land) and
// the Transmission RPC (the download-dir reported back to the *arr) so the two
// always agree on the path the *arr will import from.
func PromoteDir(sharedDir, category string) string {
	if cat := sanitizeFolderName(category); category != "" && cat != "download" {
		return filepath.Join(sharedDir, cat)
	}
	return sharedDir
}

// moveDownloadedFile moves the completed file (final or leftover .part) for
// relPath from dataDir into destDir, returning the destination path. The dst
// always uses the final name, never .part. onBytes (nil-safe) receives the bytes
// copied so the caller can report transfer progress.
func moveDownloadedFile(dataDir, destDir, relPath string, onBytes func(int64)) (string, error) {
	dst := filepath.Join(destDir, filepath.Base(relPath))
	src := resolveCompletedSrc(dataDir, relPath)
	if src == "" {
		// Source isn't in the cache. If it's already at the destination, the move
		// was done on a previous attempt (or the file was downloaded straight to
		// bulk via relocated storage) — idempotent success, NOT an error. Mirrors
		// the whole-torrent path (moveTreeEntry); without it, a single-file
		// completed download whose file already lives in bulk wedged the row with
		// "move failed: completed file not found in /data/streams".
		if fileExists(dst) {
			return dst, nil
		}
		return "", fmt.Errorf("completed file not found in %s for %q", dataDir, relPath)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", destDir, err)
	}
	if err := moveFileProgress(src, dst, onBytes); err != nil {
		return "", fmt.Errorf("move %s → %s: %w", src, dst, err)
	}
	return dst, nil
}

// dirHasFiles reports whether dir exists and contains at least one entry.
func dirHasFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// isOrphanedCompletion reports whether a completed download lost its moved file
// (file_path gone) while its source still sits in the cache — the fingerprint of
// a move interrupted by a restart. Such rows are re-queued on boot so the worker
// re-moves them; the cache-source guard prevents mass re-download when a
// downloadDir is merely unmounted for a moment.
func isOrphanedCompletion(d Download, dataDir string) bool {
	return d.FilePath != "" && !fileExists(d.FilePath) && dirHasFiles(filepath.Join(dataDir, d.Name))
}

// completionDest returns the per-torrent destination directory for a finished
// download. Normally downloadDir[/username]/<torrent>; but an *arr download
// (Source==SourceArr) with auto-promote enabled (and SharedDir set) goes straight
// into sharedDir/<category>/<torrent> — the Transmission-style "completed
// downloads" tree the *arr import from, catalogued by the category the *arr sent
// (no per-user subdir, matching Transmission). Returns "" when no destination is
// configured (legacy: keep the file in DataDir).
func (w *Worker) completionDest(d Download, torrentName string) string {
	base := w.completionBaseDir(d)
	if base == "" {
		return ""
	}
	return filepath.Join(base, sanitizeFolderName(torrentName))
}

// completionBaseDir returns the PARENT directory a finished download lands in,
// WITHOUT the per-torrent name segment. The torrent name isn't known until
// metadata arrives, so the download-to-bulk storage needs just the parent (it
// appends <sanitize(name)> itself inside its TorrentDirMaker). Sharing this with
// completionDest — which appends the SAME <sanitize(name)> — guarantees the
// storage writes exactly where the move expects, making the move a no-op.
// Returns "" when no destination is configured (legacy: keep in DataDir).
func (w *Worker) completionBaseDir(d Download) string {
	// A destination the user explicitly picked (#16) wins over the defaults. It was
	// validated against the user's allowed destinations at create time; the subdir
	// is re-cleaned defensively against traversal here.
	if d.DestBase != "" {
		if sub := cleanDestSubdir(d.DestSubdir); sub != "" {
			return filepath.Join(d.DestBase, sub)
		}
		return d.DestBase
	}
	if w.sharedDir != "" && d.Source == SourceArr && w.queueSettings().AutoPromoteArr {
		return PromoteDir(w.sharedDir, d.Category)
	}
	if w.downloadDir == "" {
		return ""
	}
	base := w.downloadDir
	if w.resolveUsername != nil {
		if u := w.resolveUsername(d.UserID); u != "" {
			base = filepath.Join(base, u)
		}
	}
	return base
}

// cleanDestSubdir defensively sanitizes a stored destination subdir: rejects
// absolute paths and any ".." traversal, returning a cleaned relative path or ""
// (which means "no subdir"). The subdir is already validated at create time;
// this is belt-and-suspenders against a tampered DB row.
func cleanDestSubdir(sub string) string {
	if sub == "" || filepath.IsAbs(sub) {
		return ""
	}
	clean := filepath.Clean(filepath.FromSlash(sub))
	if clean == "." || !filepath.IsLocal(clean) {
		return ""
	}
	return clean
}

// initFilePath computes the row's file_path + size at init time. For
// download-to-bulk it points into the bulk destination (where the storage writes
// the data) so streaming-of-in-progress and the post-completion finalize both
// resolve there; with no destination configured it's the legacy cache path under
// DataDir. Whole-torrent → the per-torrent dir; single/selected file → that dir
// plus the file's tree path (name root stripped, matching the storage layout).
func (w *Worker) initFilePath(d Download, t *torrent.Torrent, f *torrent.File, name string) (string, int64) {
	if base := w.completionBaseDir(d); base != "" {
		dir := filepath.Join(base, sanitizeFolderName(name)) // == completionDest(d, name)
		if f != nil {
			return filepath.Join(dir, bulkRel(name, f)), f.Length()
		}
		return dir, t.Length()
	}
	if f != nil {
		return filepath.Join(w.dataDir, f.Path()), f.Length()
	}
	return filepath.Join(w.dataDir, name), t.Length()
}

// bulkRel returns a torrent file's path relative to its per-torrent dir, matching
// the download storage layout: the internal tree WITHOUT the torrent-name root
// (single-file torrents have no root, so it's just the file name).
func bulkRel(name string, f *torrent.File) string {
	return filepath.FromSlash(strings.TrimPrefix(f.Path(), name+"/"))
}

// moveCompletedFile relocates a finished download from the streaming cache to the
// dedicated downloadDir (per-user, per-torrent folder). Takes the torrent-relative
// path + name as strings (not the *trackedDL) so it stays unit-testable. Returns
// an error (instead of failing silently) so the caller only flips the row to
// "completed" when the file actually reached its home — handling the case where
// the anacrolix storage left a complete ".part" that wasn't renamed yet.
func (w *Worker) moveCompletedFile(d Download, relPath, torrentName string, job *transfer.Job) (string, error) {
	destDir := w.completionDest(d, torrentName)
	if destDir == "" {
		return "", nil
	}
	dst, err := moveDownloadedFile(w.dataDir, destDir, relPath, job.AddBytesFunc())
	if err != nil {
		return "", err
	}
	job.FileDone()
	if err := w.store.SetFilePath(d.UserID, d.ID, dst); err != nil {
		log.Printf("downloads: failed to set file path for download %d: %v", d.ID, err)
	}
	w.streamer.UnregisterDownload(torrentName)
	log.Printf("downloads: moved #%d %q → %s", d.ID, torrentName, dst)
	return dst, nil
}

// moveCompletedTorrent relocates EVERY file of a finished whole-torrent
// download from the streaming cache into downloadDir/<user>/<torrent>/,
// preserving the directory structure inside the torrent. Returns the torrent's
// destination directory (persisted as the row's file_path). Same contract as
// moveCompletedFile: an error means nothing was flipped to completed and the
// next tick retries — moveCompletedTree is idempotent, so a retry (or the
// boot-time orphan re-queue) skips files that already reached the destination.
func (w *Worker) moveCompletedTorrent(d Download, td *trackedDL) (string, error) {
	return w.moveCompletedTorrentFiles(d, td.name, wholeTorrentRelPaths(td.whole.Files()), nil)
}

// moveCompletedTorrentFiles is moveCompletedTorrent's core, taking the already-
// resolved torrent-relative paths (so it runs off the tick without the live
// torrent) and a transfer.Job for progress. Same idempotent/error contract.
func (w *Worker) moveCompletedTorrentFiles(d Download, torrentName string, relPaths []string, job *transfer.Job) (string, error) {
	destDir := w.completionDest(d, torrentName)
	if destDir == "" {
		return "", nil
	}
	if err := moveCompletedTree(w.dataDir, destDir, torrentName, relPaths, job.AddBytesFunc(), job.FileDone); err != nil {
		return "", err
	}
	if err := w.store.SetFilePath(d.UserID, d.ID, destDir); err != nil {
		log.Printf("downloads: failed to set file path for download %d: %v", d.ID, err)
	}
	w.streamer.UnregisterDownload(torrentName)
	log.Printf("downloads: moved whole torrent #%d %q (%d files) → %s", d.ID, torrentName, len(relPaths), destDir)
	return destDir, nil
}

// moveCompletedTree moves every torrent-relative path from dataDir into
// destDir, keeping the structure inside the torrent. The leading
// "<torrentName>/" segment is stripped (destDir already carries the per-torrent
// folder). Idempotent: a file whose source is gone but whose destination exists
// was moved by a previous (interrupted) attempt and is skipped.
func moveCompletedTree(dataDir, destDir, torrentName string, relPaths []string, onBytes func(int64), onFileDone func()) error {
	for _, rel := range relPaths {
		moved, err := moveTreeEntry(dataDir, destDir, torrentName, rel, onBytes)
		if err != nil {
			return err
		}
		if moved && onFileDone != nil {
			onFileDone() // a relocated (or already-present) file counts toward X/Y
		}
	}
	return nil
}

// moveTreeEntry relocates one torrent-relative file into destDir. moved=true when
// a file was moved OR already sat at the destination (a prior attempt); moved=
// false for a skipped BEP 47 pad entry. Idempotent — safe to re-run after an
// interrupted move.
func moveTreeEntry(dataDir, destDir, torrentName, rel string, onBytes func(int64)) (bool, error) {
	if isPadPath(torrentName, rel) {
		return false, nil
	}
	dst, err := wholeTorrentDest(destDir, torrentName, rel)
	if err != nil {
		return false, err
	}
	src := resolveCompletedSrc(dataDir, rel)
	if src == "" {
		if fileExists(dst) {
			return true, nil // already moved on a previous attempt
		}
		return false, fmt.Errorf("completed file not found in %s for %q", dataDir, rel)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, fmt.Errorf("mkdir for %q: %w", rel, err)
	}
	if err := moveFileProgress(src, dst, onBytes); err != nil {
		return false, fmt.Errorf("move %s → %s: %w", src, dst, err)
	}
	return true, nil
}

// wholeTorrentDest resolves the destination path for one torrent-relative file,
// rejecting metadata-supplied paths that would escape destDir (".." traversal —
// torrent metadata is untrusted input).
func wholeTorrentDest(destDir, torrentName, rel string) (string, error) {
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("unsafe path %q in torrent", rel)
	}
	rel = strings.TrimPrefix(rel, torrentName+"/")
	// Re-validate AFTER the strip: "Name/../x" is lexically local as a whole
	// (it cleans to "x") but escapes destDir once the leading "Name/" is gone.
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("unsafe path %q in torrent", rel)
	}
	return filepath.Join(destDir, filepath.FromSlash(rel)), nil
}

// isPadPath reports whether a torrent-relative path is a BEP 47 padding entry
// by the ".pad/" naming convention (with or without the torrent's root folder
// prefix). Pad files exist only to piece-align the real content and may never
// be materialized on disk — trying to move one would fail every completion
// retry, wedging the download in `downloading` forever.
func isPadPath(torrentName, rel string) bool {
	rel = strings.TrimPrefix(rel, torrentName+"/")
	return strings.HasPrefix(rel, ".pad/")
}

// aiRenameCompleted re-organizes a completed download into a Plex-style path
// under downloadDir, using the AI+TMDB rename chain — the same one the promote
// flow uses. Runs in its own goroutine (off the tick loop) and is best-effort:
// any failure leaves the file where moveCompletedFile already put it. Only
// invoked when an AI client is configured ("se a IA estiver disponível").
func (w *Worker) aiRenameCompleted(d Download, currentPath string) {
	base := w.downloadDir
	if w.resolveUsername != nil {
		if u := w.resolveUsername(d.UserID); u != "" {
			base = filepath.Join(base, u)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	preview, err := renamer.GeneratePreview(ctx, w.aiClient, w.tmdbClient, filepath.Base(currentPath))
	if err != nil || preview == nil || preview.TargetPath == "" {
		return
	}
	targetRel := renamer.ResolveTargetConflict(base, preview.TargetPath)
	newDst := filepath.Join(base, targetRel)
	if newDst == currentPath {
		return
	}
	if err := os.MkdirAll(filepath.Dir(newDst), 0o755); err != nil {
		log.Printf("downloads: AI-rename mkdir #%d: %v", d.ID, err)
		return
	}
	var size int64
	if st, e := os.Stat(currentPath); e == nil {
		size = st.Size()
	}
	job := w.tracker.Start(filepath.Base(newDst), "ai-rename", 1, size)
	if err := moveFileProgress(currentPath, newDst, job.AddBytesFunc()); err != nil {
		job.Fail(err)
		log.Printf("downloads: AI-rename move #%d: %v", d.ID, err)
		return
	}
	job.FileDone()
	job.Done()
	if err := w.store.SetFilePath(d.UserID, d.ID, newDst); err != nil {
		log.Printf("downloads: AI-rename set path #%d: %v", d.ID, err)
		return
	}
	// The per-torrent folder moveCompletedFile created is now empty — tidy it.
	_ = os.Remove(filepath.Dir(currentPath))
	log.Printf("downloads: AI-renamed #%d → %s", d.ID, newDst)
}

// moveFileWithFallback renames src→dst, falling back to copy+remove across
// filesystems (EXDEV). Mirrors the promote move semantics. Delegates to
// moveFileProgress (no progress reporting); aiRenameCompleted uses the metered
// form directly for the Transfers dock.
func moveFileWithFallback(src, dst string) error { return moveFileProgress(src, dst, nil) }

// sanitizeFolderName turns a torrent name into ONE safe path segment for the
// per-torrent destination folder: strips path separators and traversal, drops
// control chars, trims trailing dots/spaces, and caps the length. Never returns
// "", ".", or ".." (which would escape or no-op the join) — falls back to "download".
func sanitizeFolderName(name string) string {
	// Neutralize path separators (a single segment can't traverse without them);
	// `.`/`..` are handled by the trailing-trim + the final guard below.
	name = strings.NewReplacer("/", "_", "\\", "_").Replace(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if len(name) > 200 {
		name = name[:200]
	}
	name = strings.TrimRight(strings.TrimSpace(name), ". ")
	if name == "" || name == "." || name == ".." {
		return "download"
	}
	return name
}

// initDownload resolves the magnet, waits for metadata, marks the target file
// for full download, and (on success) promotes the row into `tracked`. Runs in
// its own goroutine so a slow swarm never blocks the tick loop. Transient
// failures are retried on later ticks up to maxInitRetries; a context cancel
// (download paused/cancelled, or worker stopping) silently aborts without
// touching the row's status.
func (w *Worker) initDownload(ctx context.Context, d Download) {
	defer w.doneWG.Done()
	defer func() {
		w.mu.Lock()
		delete(w.pending, d.ID)
		// Clear any deletion tombstone now that THIS init has fully exited: the
		// resurrection window is closed (we either bailed or promoted under the
		// lock above), so an ID reused by a later Create starts clean. Done here
		// (always-run defer) so the tombstone never leaks when init bails before
		// the promotion guard (e.g. EnsureActive failed).
		delete(w.removed, d.ID)
		w.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	// EffectiveMagnet is the active alternative source when rotation has switched
	// away from the original, otherwise the original magnet. A common failure is
	// an ephemeral indexer .torrent URL (Jackett /dl/...) that has since 404'd —
	// ensureActiveWithFallback recovers via a bare info_hash magnet.
	hash, err := w.ensureActiveWithFallback(ctx, &d)
	if err != nil {
		w.failOrRetry(d, "load torrent: "+err.Error())
		return
	}
	t, ok := w.streamer.Client().Torrent(hash)
	if !ok {
		w.failOrRetry(d, "torrent gone after EnsureActive")
		return
	}
	// Block waiting for metadata so the file slice is populated. ctx already
	// carries the 90s deadline, so we lean on it instead of a second timer.
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		w.failOrRetry(d, "timeout aguardando metadados")
		return
	}

	f, whole, ok := w.initTarget(&d, hash, t)
	if !ok {
		return
	}

	name := t.Name()
	w.streamer.RegisterDownload(name)
	// Persist resolved torrent metadata. file_path GRAVA ABSOLUTO (dataDir + path
	// dentro do torrent) — não relativo. Antes guardava só `f.Path()` (relativo,
	// ex.: "Folder/file.mkv"); se o move pós-completion falhava (cross-mount,
	// container OOM no meio do copy), o file_path ficava inválido pra qualquer
	// consumer (Local browser, Promote, etc.). Absoluto: se move sucede,
	// SetFilePath sobrescreve com o destino; se falha, ainda dá pra achar o
	// arquivo na cache pelo path. Whole-torrent: a raiz do torrent na cache e o
	// tamanho agregado.
	filePath, fileSize := w.initFilePath(d, t, f, name)
	if err := w.store.UpdateMetadata(d.UserID, d.ID, name, filePath, fileSize); err != nil {
		log.Printf("downloads: failed to update metadata for download %d: %v", d.ID, err)
	}

	now := time.Now()
	td := &trackedDL{
		id:             d.ID,
		userID:         d.UserID,
		infoHash:       d.InfoHash,
		hash:           hash,
		torrent:        t,
		file:           f,
		whole:          whole,
		name:           name,
		startedAt:      now,
		lastProgressAt: now,
	}
	td.lastProgressBytes, _, _ = td.progress()
	if !w.promoteOrAbort(d, td, name) {
		return
	}
	// Snapshot inicial dos bytes já completos. Sem isso, o usuário que clica
	// Download enquanto está streamando vê 0% nos primeiros 2-4s (entre o
	// init terminar e o primeiro tick rodar UpdateProgress) — interpreta como
	// "recomeçou". VerifyFile acima já reconciliou o estado de pieces, então
	// BytesCompleted aqui reflete a realidade do disco.
	initialBytes, totalBytes, _ := td.progress()
	if initialBytes > 0 {
		if err := w.store.UpdateProgress(d.UserID, d.ID, initialBytes); err != nil {
			log.Printf("downloads: failed to update initial progress for download %d: %v", d.ID, err)
		}
	}
	log.Printf("downloads: started #%d %q (file %d, %d bytes, completed=%d)", d.ID, name, d.FileIndex, totalBytes, initialBytes)
}

// promoteOrAbort moves a freshly-initialized download into `tracked` UNLESS it
// was cancelled or deleted while init was resolving metadata. Returns false
// (without promoting) in two cases:
//
//   - `pending` no longer holds our entry: the tick loop or Remove() deleted it
//     and called cancel (paused/cancelled/preempted/deleted).
//   - `removed` holds a tombstone: Remove() deleted the row while we were
//     resolving metadata. Re-promoting here would RESURRECT a row the user just
//     deleted — the intermittent "Remove didn't remove" window this fix closes.
//
// On abort it undoes the eviction protection initDownload speculatively
// registered. The tombstone itself is cleared by initDownload's deferred
// cleanup once this goroutine exits.
func (w *Worker) promoteOrAbort(d Download, td *trackedDL, name string) bool {
	w.mu.Lock()
	_, stillPending := w.pending[d.ID]
	_, tombstoned := w.removed[d.ID]
	if !stillPending || tombstoned {
		w.mu.Unlock()
		w.streamer.UnregisterDownload(name)
		return false
	}
	w.tracked[d.ID] = td
	delete(w.retries, d.ID)
	w.mu.Unlock()
	return true
}

// initTarget marks the row's download target as wanted in anacrolix and
// returns it: a single *torrent.File for per-file rows, or the torrent itself
// (as a wholeTarget) for FileIndexWholeTorrent rows. ok=false means the row was
// already flipped to failed (no files in torrent).
//
// Both paths hash-check pieces no disco ANTES de marcar como wanted. Sem isso,
// se o shutdown anterior foi ungraceful (SIGKILL pelo Docker antes do
// graceful-shutdown ficar pronto), o bolt DB do anacrolix está stale — pieces
// existem no disco mas anacrolix os marca como incompletos e pediria esses
// bytes do swarm de novo. VerifyFile/VerifyTorrent hasheiam cada piece e marcam
// como Complete os que casam (idempotente, dedupe por processo).
func (w *Worker) initTarget(d *Download, hash metainfo.Hash, t wholeTarget) (*torrent.File, wholeTarget, bool) {
	if d.IsWholeTorrent() {
		if err := w.streamer.VerifyTorrent(hash); err != nil {
			log.Printf("downloads: failed to verify torrent pieces for download %d: %v", d.ID, err)
		}
		// DownloadAll sets piece priority to Normal across the whole torrent —
		// anacrolix schedules every file to completion. ONE queue row, ONE slot.
		t.DownloadAll()
		return nil, t, true
	}
	files := t.Files()
	fileIdx, okResolved := w.resolveFileIndex(d, files)
	if !okResolved {
		return nil, nil, false
	}
	f := files[fileIdx]
	if err := w.streamer.VerifyFile(hash, fileIdx); err != nil {
		log.Printf("downloads: failed to verify file pieces for download %d: %v", d.ID, err)
	}
	// File.Download() sets piece priority to Normal across the file's piece
	// range — anacrolix then schedules a full download to completion.
	f.Download()
	return f, nil, true
}

// resolveFileIndex resolves the target file index in a torrent. If index is out of bounds,
// it auto-picks the best file. Returns the resolved index and true on success.
func (w *Worker) resolveFileIndex(d *Download, files []*torrent.File) (int, bool) {
	fileIdx := d.FileIndex
	if fileIdx >= 0 && fileIdx < len(files) {
		return fileIdx, true
	}
	// Auto-pick: FileIndex == -1 means "pick the best file".
	// We prefer the largest video/media file, or fall back to the largest file overall.
	fileIdx = pickBestFile(files)
	if fileIdx < 0 {
		if err := w.store.SetError(d.UserID, d.ID, "no files in torrent"); err != nil {
			log.Printf("downloads: failed to set error status for download %d: %v", d.ID, err)
		}
		return -1, false
	}
	// Persist the resolved FileIndex so subsequent ticks don't re-pick.
	if d.FileIndex != fileIdx {
		if err := w.store.SetFileIndex(d.UserID, d.ID, fileIdx); err != nil {
			log.Printf("downloads: failed to set file index for download %d: %v", d.ID, err)
		}
		d.FileIndex = fileIdx
	}
	return fileIdx, true
}

// ensureActiveWithFallback loads the torrent for a download, recovering from a
// dead primary source. Indexer .torrent links (Jackett /dl/...) are ephemeral —
// once the token/cache expires they 404, and a row whose stored "magnet" is
// actually such a URL would fail init forever. When that happens and the
// info_hash is known, we retry with a bare magnet (DHT + the streamer's injected
// public trackers resolve it) and persist it so later retries/reboots skip the
// dead URL.
func (w *Worker) ensureActiveWithFallback(ctx context.Context, d *Download) (metainfo.Hash, error) {
	src := d.EffectiveMagnet()
	hash, err := w.ensureActive(ctx, *d, src)
	if err == nil {
		return hash, nil
	}
	alt, ok := fallbackMagnet(src, d.InfoHash)
	if !ok {
		return hash, err
	}
	log.Printf("downloads: #%d source failed (%v) — retrying via info_hash magnet", d.ID, err)
	h2, err2 := w.ensureActive(ctx, *d, alt)
	if err2 != nil {
		return hash, fmt.Errorf("%v; fallback por info_hash também falhou: %w", err, err2)
	}
	if uerr := w.store.SetActiveMagnet(d.UserID, d.ID, alt); uerr != nil {
		log.Printf("downloads: #%d persist fallback magnet failed: %v", d.ID, uerr)
	} else {
		d.ActiveMagnet = alt
	}
	return h2, nil
}

// ensureActive adds the torrent, writing its data DIRECTLY to the configured
// bulk destination (download-to-bulk) when one is set — so torrents larger than
// the SSD cache don't overflow it and the move-on-completion is a no-op. With no
// destination configured it falls back to the cache (legacy streaming storage).
// The BaseDir is the destination PARENT (without the torrent-name segment); the
// storage appends <sanitizeFolderName(name)> itself once metadata resolves the
// real name — keeping the write path identical to completionDest.
func (w *Worker) ensureActive(ctx context.Context, d Download, src string) (metainfo.Hash, error) {
	base := w.completionBaseDir(d)
	if base == "" {
		return w.streamer.EnsureActive(ctx, src)
	}
	return w.streamer.EnsureActiveForDownload(ctx, src, streamer.DownloadStorageSpec{
		BaseDir:  base,
		Sanitize: sanitizeFolderName,
	})
}

// fallbackMagnet returns a bare info_hash magnet when src is an http(s) URL (an
// ephemeral indexer .torrent link) and a 40-hex info_hash is known. ok is false
// when no fallback applies — src is already a magnet, or the hash is missing.
func fallbackMagnet(src, infoHash string) (magnet string, ok bool) {
	if infoHash == "" {
		return "", false
	}
	low := strings.ToLower(strings.TrimSpace(src))
	if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
		return "", false
	}
	return "magnet:?xt=urn:btih:" + infoHash, true
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

// renameFn is os.Rename, overridable in tests to force the cross-filesystem copy
// fallback (EXDEV can't be reproduced within a single temp dir).
var renameFn = os.Rename

// moveFile moves src to dst with no progress reporting (see moveFileProgress).
func moveFile(src, dst string) error { return moveFileProgress(src, dst, nil) }

// moveFileProgress moves src to dst. Tries os.Rename first (cheap, same-
// filesystem; reports the file size as one chunk so a same-fs move still shows
// 100% on the progress bar); falls back to copy+delete for cross-filesystem moves
// (DataDir on one volume, DownloadDir on another), streaming through a
// transfer.ProgressReader so onBytes (nil-safe) sees the copy advance.
func moveFileProgress(src, dst string, onBytes func(int64)) error {
	if err := renameFn(src, dst); err == nil {
		if onBytes != nil {
			if st, e := os.Stat(dst); e == nil {
				onBytes(st.Size())
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, transfer.ProgressReader(in, onBytes)); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return os.Remove(src)
}
