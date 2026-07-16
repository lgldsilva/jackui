package downloads

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/lgldsilva/jackui/internal/transfer"
)

// sampleProgress persists the current byte count and resets the stall-detection
// clock whenever bytes advance. A transient regression (completed < DB) is
// logged but not persisted — anacrolix can briefly report fewer bytes during a
// piece re-check.
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
	w.tracker.SubmitFor(d.UserID, name, "download-move", len(relPaths), total, func(job *transfer.Job) {
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
			// #nosec G104 -- Close best-effort no cleanup; erro no teardown irrelevante
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
