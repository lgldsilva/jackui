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

	"github.com/luizg/jackui/internal/streamer"
)

// maxInitRetries caps how many times a slow/dead magnet is retried (in memory)
// before the download is marked failed. Each retry happens on a later tick, so
// transient swarm hiccups self-heal without the user re-queueing manually.
const maxInitRetries = 3

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
	ntfyBaseURL  string // default https://ntfy.sh
	ntfyTopic    string // global default topic; per-user override via store
	ntfyClient   *http.Client
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
// negative. dataDir is the streamer's piece-storage directory; downloadDir is
// where completed files are moved (empty string keeps the legacy behaviour of
// leaving files in DataDir protected from eviction). ntfyBaseURL and ntfyTopic
// configure push notifications; pass empty strings to disable.
func NewWorker(store *Store, s *streamer.Streamer, dataDir, downloadDir string, interval time.Duration, ntfyBaseURL, ntfyTopic string) *Worker {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if ntfyBaseURL == "" {
		ntfyBaseURL = "https://ntfy.sh"
	}
	w := &Worker{
		store:        store,
		streamer:     s,
		dataDir:      dataDir,
		downloadDir:  downloadDir,
		interval:     interval,
		tracked:      make(map[int]*trackedDL),
		pending:      make(map[int]context.CancelFunc),
		retries:      make(map[int]int),
		stop:         make(chan struct{}),
		ntfyBaseURL:  ntfyBaseURL,
		ntfyTopic:    ntfyTopic,
		ntfyClient:   &http.Client{Timeout: 10 * time.Second},
	}
	// Pre-register eviction protection for active downloads. Completed
	// downloads are only protected when no dedicated downloadDir is configured
	// (legacy mode) — in that case the file lives in DataDir and must not be
	// evicted. When downloadDir is set, completed files have already been
	// moved there so DataDir pieces can be freed by the LRU.
	if all, err := store.ListAll(); err == nil {
		for _, d := range all {
			if d.Status == StatusFailed || d.Name == "" {
				continue
			}
			if d.Status == StatusCompleted && downloadDir != "" {
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
	// (user paused/cancelled). Cancel in-flight inits too so a cancelled
	// download stops resolving metadata immediately.
	w.mu.Lock()
	for id, td := range w.tracked {
		if !wantIDs[id] {
			w.streamer.UnregisterDownload(td.name)
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
	_, ok := w.streamer.Client().Torrent(td.hash)
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
		_ = w.store.UpdateProgress(d.UserID, d.ID, completed)
	}
}

func (w *Worker) checkCompletion(d Download, td *trackedDL) {
	if td.file == nil {
		return
	}
	completed := td.file.BytesCompleted()
	if completed < td.file.Length() || td.file.Length() <= 0 {
		return
	}
	w.moveCompletedFile(d, td)
	_ = w.store.SetStatus(d.UserID, d.ID, StatusCompleted)
	w.mu.Lock()
	delete(w.tracked, d.ID)
	w.mu.Unlock()
	log.Printf("downloads: completed #%d %q", d.ID, td.name)
	body := fmt.Sprintf("%s · %.2f MB", td.name, float64(td.file.Length())/1048576)
	go w.sendNtfy(context.Background(), "Download concluído: "+td.name, body, "white_check_mark,torrent")
}

func (w *Worker) moveCompletedFile(d Download, td *trackedDL) {
	if w.downloadDir == "" {
		return
	}
	src := filepath.Join(w.dataDir, td.file.Path())
	dst := filepath.Join(w.downloadDir, filepath.Base(td.file.Path()))
	if mkErr := os.MkdirAll(w.downloadDir, 0755); mkErr != nil {
		log.Printf("downloads: mkdir %s: %v", w.downloadDir, mkErr)
		return
	}
	if mvErr := moveFile(src, dst); mvErr != nil {
		log.Printf("downloads: move #%d %q → %s: %v", d.ID, td.name, dst, mvErr)
		return
	}
	_ = w.store.SetFilePath(d.UserID, d.ID, dst)
	w.streamer.UnregisterDownload(td.name)
	log.Printf("downloads: moved #%d %q → %s", d.ID, td.name, dst)
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

	hash, err := w.streamer.EnsureActive(ctx, d.Magnet)
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
	if d.FileIndex < 0 || d.FileIndex >= len(files) {
		// Permanent error — bad index won't fix itself on retry.
		_ = w.store.SetError(d.UserID, d.ID, "file index fora do intervalo")
		return
	}
	f := files[d.FileIndex]
	// Hash-check pieces no disco ANTES de marcar como wanted. Sem isso,
	// se o shutdown anterior foi ungraceful (SIGKILL pelo Docker antes do
	// graceful-shutdown ficar pronto), o bolt DB do anacrolix está stale —
	// pieces existem no disco mas anacrolix os marca como incompletos. f.Download
	// abaixo iria pedir esses bytes do swarm. VerifyFile faz o hash de cada
	// piece e marca como Complete os que casam, eliminando re-download. Idempotente
	// (sync.Map dedupe entre streaming e download). Custo: ~1 hash por piece.
	_ = w.streamer.VerifyFile(hash, d.FileIndex)
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
	_ = w.store.UpdateMetadata(d.UserID, d.ID, name, filePath, fileSize)

	td := &trackedDL{
		id:        d.ID,
		infoHash:  d.InfoHash,
		hash:      hash,
		torrent:   t,
		file:      f,
		name:      name,
		startedAt: time.Now(),
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
		_ = w.store.UpdateProgress(d.UserID, d.ID, initialBytes)
	}
	log.Printf("downloads: started #%d %q (file %d, %d bytes, completed=%d)", d.ID, name, d.FileIndex, f.Length(), f.BytesCompleted())
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
		_ = w.store.SetError(d.UserID, d.ID, msg)
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
