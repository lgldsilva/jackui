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

	"github.com/luizg/jackui/internal/jackett"
	"github.com/luizg/jackui/internal/streamer"
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
	MaxActive         int // GLOBAL ceiling: concurrent downloads across all users (streaming excluded)
	PerUserMaxActive  int // per-user concurrent cap; 0 = no per-user limit
	StallThresholdMin int // minutes with no progress AND no seeders before a demote
	MaxStalls         int // stalls before the download is paused (0 = never pause, cycle forever)
	AgingStepMin      int  // queue aging: minutes of waiting per +1 bonus (0 disables)
	AgingCap          int  // ceiling on the aging bonus
	RotationEnabled   bool // Phase 2: on a no-seed stall, try alternative sources before demoting
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
	Interval        time.Duration
	NtfyBaseURL     string                // default https://ntfy.sh
	NtfyTopic       string                // global default topic; per-user override via store
	NtfyToken       string                // optional access token for protected topics (Authorization: Bearer)
	ResolveUsername func(int) string      // optional username resolver for per-user subdir
	Settings        func() QueueSettings  // live queue settings; nil → DefaultQueueSettings
	Jackett         sourceSearcher        // Phase 2 source rotation; nil disables Jackett re-search
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
	interval    time.Duration

	mu      sync.Mutex
	tracked map[int]*trackedDL         // fully initialized, being sampled — by download.ID
	pending map[int]context.CancelFunc // init goroutine in flight — cancel on removal/stop
	retries map[int]int                // transient init failures per download.ID

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
}

type trackedDL struct {
	id                int
	userID            int
	infoHash          string
	hash              metainfo.Hash
	torrent           *torrent.Torrent
	file              *torrent.File
	name              string
	startedAt         time.Time
	lastProgressBytes int64     // bytes at the last forward sample (stall detection)
	lastProgressAt    time.Time // when bytes last advanced
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
		interval:        cfg.Interval,
		tracked:         make(map[int]*trackedDL),
		pending:         make(map[int]context.CancelFunc),
		retries:         make(map[int]int),
		stop:            make(chan struct{}),
		ntfyBaseURL:     cfg.NtfyBaseURL,
		ntfyTopic:       cfg.NtfyTopic,
		ntfyToken:       cfg.NtfyToken,
		ntfyClient:      &http.Client{Timeout: 10 * time.Second},
		resolveUsername: cfg.ResolveUsername,
		settings:        cfg.Settings,
		jackett:         cfg.Jackett,
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
		if d.Status == StatusFailed || d.Name == "" {
			continue
		}
		if d.Status == StatusCompleted && cfg.DownloadDir != "" {
			if isOrphanedCompletion(d, cfg.DataDir) {
				if err := cfg.Store.SetStatus(d.UserID, d.ID, StatusQueued); err != nil {
					log.Printf("downloads: failed to set status queued for existing download %d: %v", d.ID, err)
				}
				cfg.Streamer.RegisterDownload(d.Name)
				log.Printf("downloads: re-queued orphan #%d %q (file_path missing, source still in cache)", d.ID, d.Name)
			}
			continue
		}
		cfg.Streamer.RegisterDownload(d.Name)
	}
}

// Start launches the worker loop in a goroutine. Idempotent on the caller side
// — call once. Returns immediately.
func (w *Worker) Start() {
	w.doneWG.Add(1)
	go w.run()
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
	for id, td := range w.tracked {
		if !wantIDs[id] {
			w.unregisterLocked(td)
			delete(w.tracked, id)
			delete(w.retries, id)
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
	if td.file == nil {
		return
	}
	completed := td.file.BytesCompleted()
	if completed < d.BytesDownloaded {
		log.Printf("downloads: ignoring transient regression #%d %q completed %d → %d (keeping DB) — peers=%d",
			d.ID, td.name, d.BytesDownloaded, completed, len(td.torrent.PeerConns()))
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

func (w *Worker) checkCompletion(d Download, td *trackedDL) {
	if td.file == nil {
		return
	}
	completed := td.file.BytesCompleted()
	if completed < td.file.Length() || td.file.Length() <= 0 {
		return
	}
	if err := w.moveCompletedFile(d, td.file.Path(), td.name); err != nil {
		// Don't flip to completed — the file never reached the destination. Retry
		// next tick (e.g. the anacrolix storage is still renaming the .part).
		// Avoids a phantom "completed" whose file_path points to a missing file.
		log.Printf("downloads: completion move failed #%d %q: %v (retry next tick)", d.ID, td.name, err)
		return
	}
	if err := w.store.SetStatus(d.UserID, d.ID, StatusCompleted); err != nil {
		log.Printf("downloads: failed to set status completed for download %d: %v", d.ID, err)
	}
	w.mu.Lock()
	delete(w.tracked, d.ID)
	w.mu.Unlock()
	log.Printf("downloads: completed #%d %q", d.ID, td.name)
	body := fmt.Sprintf("%s · %.2f MB", td.name, float64(td.file.Length())/1048576)
	go w.sendNtfy(context.Background(), "Download concluído: "+td.name, body, "white_check_mark,torrent")
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

// moveDownloadedFile moves the completed file (final or leftover .part) for
// relPath from dataDir into destDir, returning the destination path. The dst
// always uses the final name, never .part.
func moveDownloadedFile(dataDir, destDir, relPath string) (string, error) {
	src := resolveCompletedSrc(dataDir, relPath)
	if src == "" {
		return "", fmt.Errorf("completed file not found in %s for %q", dataDir, relPath)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", destDir, err)
	}
	dst := filepath.Join(destDir, filepath.Base(relPath))
	if err := moveFile(src, dst); err != nil {
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

// moveCompletedFile relocates a finished download from the streaming cache to the
// dedicated downloadDir (per-user, per-torrent folder). Takes the torrent-relative
// path + name as strings (not the *trackedDL) so it stays unit-testable. Returns
// an error (instead of failing silently) so the caller only flips the row to
// "completed" when the file actually reached its home — handling the case where
// the anacrolix storage left a complete ".part" that wasn't renamed yet.
func (w *Worker) moveCompletedFile(d Download, relPath, torrentName string) error {
	if w.downloadDir == "" {
		return nil
	}
	username := ""
	if w.resolveUsername != nil {
		username = w.resolveUsername(d.UserID)
	}
	destDir := completedDestDir(w.downloadDir, username, torrentName)
	dst, err := moveDownloadedFile(w.dataDir, destDir, relPath)
	if err != nil {
		return err
	}
	if err := w.store.SetFilePath(d.UserID, d.ID, dst); err != nil {
		log.Printf("downloads: failed to set file path for download %d: %v", d.ID, err)
	}
	w.streamer.UnregisterDownload(torrentName)
	log.Printf("downloads: moved #%d %q → %s", d.ID, torrentName, dst)
	return nil
}

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
		w.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	// EffectiveMagnet is the active alternative source when rotation has switched
	// away from the original, otherwise the original magnet.
	hash, err := w.streamer.EnsureActive(ctx, d.EffectiveMagnet())
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

	files := t.Files()
	fileIdx, okResolved := w.resolveFileIndex(&d, files)
	if !okResolved {
		return
	}
	f := files[fileIdx]
	// Hash-check pieces no disco ANTES de marcar como wanted. Sem isso,
	// se o shutdown anterior foi ungraceful (SIGKILL pelo Docker antes do
	// graceful-shutdown ficar pronto), o bolt DB do anacrolix está stale —
	// pieces existem no disco mas anacrolix os marca como incompletos. f.Download
	// abaixo iria pedir esses bytes do swarm. VerifyFile faz o hash de cada
	// piece e marca como Complete os que casam, eliminando re-download. Idempotente
	// (sync.Map dedupe entre streaming e download). Custo: ~1 hash por piece.
	if err := w.streamer.VerifyFile(hash, fileIdx); err != nil {
		log.Printf("downloads: failed to verify file pieces for download %d: %v", d.ID, err)
	}
	// File.Download() sets piece priority to Normal across the file's piece
	// range — anacrolix then schedules a full download to completion.
	f.Download()

	name := t.Name()
	w.streamer.RegisterDownload(name)
	// Persist resolved torrent metadata. file_path GRAVA ABSOLUTO (dataDir + path
	// dentro do torrent) — não relativo. Antes guardava só `f.Path()` (relativo,
	// ex.: "Folder/file.mkv"); se o move pós-completion falhava (cross-mount,
	// container OOM no meio do copy), o file_path ficava inválido pra qualquer
	// consumer (Local browser, Promote, etc.). Absoluto: se move sucede,
	// SetFilePath sobrescreve com o destino; se falha, ainda dá pra achar o
	// arquivo na cache pelo path.
	filePath := filepath.Join(w.dataDir, f.Path())
	fileSize := f.Length()
	if err := w.store.UpdateMetadata(d.UserID, d.ID, name, filePath, fileSize); err != nil {
		log.Printf("downloads: failed to update metadata for download %d: %v", d.ID, err)
	}

	now := time.Now()
	td := &trackedDL{
		id:                d.ID,
		userID:            d.UserID,
		infoHash:          d.InfoHash,
		hash:              hash,
		torrent:           t,
		file:              f,
		name:              name,
		startedAt:         now,
		lastProgressBytes: f.BytesCompleted(),
		lastProgressAt:    now,
	}
	w.mu.Lock()
	// If the download was cancelled mid-init, `pending` no longer holds our
	// entry (the tick loop deleted it and called cancel). Don't promote it —
	// undo the eviction protection we just registered.
	if _, stillPending := w.pending[d.ID]; !stillPending {
		w.mu.Unlock()
		w.streamer.UnregisterDownload(name)
		return
	}
	w.tracked[d.ID] = td
	delete(w.retries, d.ID)
	w.mu.Unlock()
	// Snapshot inicial dos bytes já completos. Sem isso, o usuário que clica
	// Download enquanto está streamando vê 0% nos primeiros 2-4s (entre o
	// init terminar e o primeiro tick rodar UpdateProgress) — interpreta como
	// "recomeçou". VerifyFile acima já reconciliou o estado de pieces, então
	// BytesCompleted aqui reflete a realidade do disco.
	if initialBytes := f.BytesCompleted(); initialBytes > 0 {
		if err := w.store.UpdateProgress(d.UserID, d.ID, initialBytes); err != nil {
			log.Printf("downloads: failed to update initial progress for download %d: %v", d.ID, err)
		}
	}
	log.Printf("downloads: started #%d %q (file %d, %d bytes, completed=%d)", d.ID, name, d.FileIndex, f.Length(), f.BytesCompleted())
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

// moveFile moves src to dst. Tries os.Rename first (cheap, same-filesystem);
// falls back to copy+delete for cross-filesystem moves (DataDir on one volume,
// DownloadDir on another).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
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
	if _, err := io.Copy(out, in); err != nil {
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
