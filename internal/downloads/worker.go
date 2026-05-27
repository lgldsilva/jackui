package downloads

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/luizg/jackui/internal/streamer"
)

// Worker reconciles download rows in the store with the running anacrolix
// torrent client. It runs a single ticker; each tick it:
//
//  1. Loads every row in status='downloading'
//  2. Ensures the underlying torrent is loaded in the streamer
//  3. Marks the target file for full download (priority = Normal across all pieces)
//  4. Samples bytes_completed and persists progress
//  5. Flips to 'completed' once all bytes are on disk
//
// The worker is singleton — start it once at boot. It owns the per-download
// state in `tracked` so we can cancel readers and unregister streamer
// protection on user-initiated cancel.
type Worker struct {
	store    *Store
	streamer *streamer.Streamer
	interval time.Duration

	mu      sync.Mutex
	tracked map[int]*trackedDL // by download.ID

	stop   chan struct{}
	doneWG sync.WaitGroup
}

type trackedDL struct {
	id        int
	infoHash  string
	hash      metainfo.Hash
	torrent   *torrent.Torrent
	file      *torrent.File
	name      string
	startedAt time.Time
}

// NewWorker constructs a worker. interval defaults to 2 seconds when zero or
// negative. As a side effect, every non-final row (downloading/completed) is
// pre-registered with the streamer's eviction protection set — that way the
// cache LRU can't blow away a torrent dir between boot and the first tick.
func NewWorker(store *Store, s *streamer.Streamer, interval time.Duration) *Worker {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	w := &Worker{
		store:    store,
		streamer: s,
		interval: interval,
		tracked:  make(map[int]*trackedDL),
		stop:     make(chan struct{}),
	}
	// Pre-register eviction protection for everything not in a final-fail
	// state. The streamer's eviction loop runs once a minute and may fire
	// before our first tick; without this, a completed download could be
	// deleted before we get a chance to re-register it.
	if all, err := store.ListAll(); err == nil {
		for _, d := range all {
			if d.Status == StatusFailed || d.Name == "" {
				continue
			}
			s.RegisterDownload(d.Name)
		}
	}
	return w
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

	// Untrack any IDs that vanished from the active set since last tick.
	w.mu.Lock()
	for id, td := range w.tracked {
		if !wantIDs[id] {
			w.streamer.UnregisterDownload(td.name)
			delete(w.tracked, id)
		}
	}
	w.mu.Unlock()

	for _, d := range active {
		w.reconcile(d)
	}
}

// reconcile brings the in-memory torrent state in line with one DB row. Always
// safe to call repeatedly — no-ops if nothing has changed since last tick.
func (w *Worker) reconcile(d Download) {
	w.mu.Lock()
	td, exists := w.tracked[d.ID]
	w.mu.Unlock()

	if !exists {
		// First time we see this row — bring up the torrent + mark the file.
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		hash, err := w.streamer.EnsureActive(ctx, d.Magnet)
		if err != nil {
			log.Printf("downloads: ensure active for #%d (%s) failed: %v", d.ID, d.InfoHash, err)
			_ = w.store.SetError(d.ID, "load torrent: "+err.Error())
			return
		}

		t, ok := w.streamer.Client().Torrent(hash)
		if !ok {
			_ = w.store.SetError(d.ID, "torrent gone after EnsureActive")
			return
		}
		// Block briefly waiting for metadata so file slice is populated.
		select {
		case <-t.GotInfo():
		case <-time.After(60 * time.Second):
			_ = w.store.SetError(d.ID, "timeout aguardando metadados")
			return
		}

		files := t.Files()
		if d.FileIndex < 0 || d.FileIndex >= len(files) {
			_ = w.store.SetError(d.ID, "file index fora do intervalo")
			return
		}
		f := files[d.FileIndex]
		// File.Download() sets piece priority to Normal across the file's
		// piece range — anacrolix will then schedule them like any normal
		// torrent download (in order, with rarest-first considerations).
		// This is exactly what we want: full download to completion.
		f.Download()

		name := t.Name()
		w.streamer.RegisterDownload(name)

		td = &trackedDL{
			id:        d.ID,
			infoHash:  d.InfoHash,
			hash:      hash,
			torrent:   t,
			file:      f,
			name:      name,
			startedAt: time.Now(),
		}
		w.mu.Lock()
		w.tracked[d.ID] = td
		w.mu.Unlock()
		log.Printf("downloads: started #%d %q (file %d, %d bytes)", d.ID, name, d.FileIndex, f.Length())
	}

	// Sample progress and persist. If the row was just created above, td.file
	// is guaranteed non-nil (we returned early on any error path).
	if td.file == nil {
		return
	}
	completed := td.file.BytesCompleted()
	if completed != d.BytesDownloaded {
		_ = w.store.UpdateProgress(d.ID, completed)
	}
	// Completion check — file.Length() is the logical total bytes.
	if completed >= td.file.Length() && td.file.Length() > 0 {
		_ = w.store.SetStatus(d.ID, StatusCompleted)
		// Keep RegisterDownload on (the file should not be evicted just because
		// it finished — the user explicitly wanted to retain it). Drop the in-
		// memory tracking so the next tick won't keep updating progress.
		w.mu.Lock()
		delete(w.tracked, d.ID)
		w.mu.Unlock()
		log.Printf("downloads: completed #%d %q", d.ID, td.name)
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
