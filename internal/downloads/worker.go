package downloads

import (
	"context"
	"log"
	"net/http"
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
