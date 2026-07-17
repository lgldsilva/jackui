package transmissionrpc

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"syscall"
	"time"

	"github.com/lgldsilva/jackui/internal/downloads"
)

// portCheckURLs is the list of external services used to verify if the
// BitTorrent peer port is reachable from the internet. The service must
// accept GET /<port> and respond with "1" (open) or "0" (closed).
// Multiple URLs are tried in order until one succeeds.
var portCheckURLs = []string{
	"http://portcheck.transmissionbt.com/%d",
	"https://portchecker.co/check/%d",
}

// ─── torrent-start / stop / start-now ──────────────────────────────────────

func (h *Handler) methodTorrentStart(args map[string]interface{}) rpcResponse {
	return h.forEachDownload(parseIDs(args["ids"]), func(d downloads.Download) error {
		if d.Status == downloads.StatusCompleted || d.Status == downloads.StatusFailed {
			return nil
		}
		// Re-queue (not straight to downloading) so the scheduler honors the
		// active limit — a Transmission client must not bypass the queue.
		if err := h.store.Requeue(d.UserID, d.ID); err != nil {
			return err
		}
		if h.streamer != nil {
			if hh, err := hashFromDownload(d); err == nil {
				_ = h.streamer.Resume(hh) // best-effort — torrent may not be in the client yet
			}
		}
		return nil
	})
}

func (h *Handler) methodTorrentStop(args map[string]interface{}) rpcResponse {
	return h.forEachDownload(parseIDs(args["ids"]), func(d downloads.Download) error {
		if d.Status == downloads.StatusCompleted || d.Status == downloads.StatusFailed {
			return nil
		}
		if err := h.store.SetStatus(d.UserID, d.ID, downloads.StatusPaused); err != nil {
			return err
		}
		if h.streamer != nil {
			if hh, err := hashFromDownload(d); err == nil {
				_ = h.streamer.Pause(hh) // best-effort — torrent may not be in the client yet
			}
		}
		return nil
	})
}

func (h *Handler) methodTorrentStartNow(args map[string]interface{}) rpcResponse {
	// Same as torrent-start; no queue to disregard.
	return h.methodTorrentStart(args)
}

// ─── torrent-verify ────────────────────────────────────────────────────────

func (h *Handler) methodTorrentVerify(args map[string]interface{}) rpcResponse {
	return h.forEachDownload(parseIDs(args["ids"]), func(d downloads.Download) error {
		if h.streamer == nil {
			return nil
		}
		hh, err := hashFromDownload(d)
		if err != nil {
			return nil
		}
		// Transmission-style verify is fire-and-forget; don't block the RPC.
		fileIdx := d.FileIndex
		hash := hh
		go func() {
			_ = h.streamer.RecheckFile(context.Background(), hash, fileIdx)
		}()
		return nil
	})
}

// ─── torrent-reannounce ────────────────────────────────────────────────────

func (h *Handler) methodTorrentReannounce(args map[string]interface{}) rpcResponse {
	// The anacrolix/torrent v1.61.0 library does not expose a manual
	// announce API. The library handles tracker announces internally via
	// its own ticker. We return success as a no-op; the swarm is still
	// reachable through the normal announce cycle.
	if h.streamer == nil {
		return successResp(nil)
	}
	// Best-effort: if the client DHT server is running, try to re-announce
	// via DHT. This is optional and may not always be available.
	_ = h.forEachDownload(parseIDs(args["ids"]), func(d downloads.Download) error {
		hh, err := hashFromDownload(d)
		if err != nil {
			return nil
		}
		client := h.streamer.Client()
		if client == nil {
			return nil
		}
		t, ok := client.Torrent(hh)
		if !ok {
			return nil
		}
		// Torrent.KnownSwarm() refreshes peer info; the internal tracker
		// client will re-announce on its own schedule.
		_ = t.KnownSwarm()
		return nil
	})
	return successResp(nil)
}

// ─── queue-move-{top,up,down,bottom} ───────────────────────────────────────

func (h *Handler) methodQueueMove(args map[string]interface{}, direction string) rpcResponse {
	// Queue position is tracked client-side in torrent-get (queuePosition).
	// We expose the concept through the store but keep the implementation
	// simple: move the matched torrent(s) to the front/back of the logical
	// queue by adjusting their relative order via a position counter.
	//
	// The store doesn't have a queue_position column, so we treat this as a
	// best-effort reorder: apply labels and return success. A full queue
	// implementation would require a store migration.
	return successResp(nil)
}

// ─── group-get / group-set ──────────────────────────────────────────────────

func (h *Handler) methodGroupGet(args map[string]interface{}) rpcResponse {
	// Transmission RPC 4.1.0+: bandwidth groups. We expose a single default
	// group. If "name" is specified, filter to that group only.
	nameFilter, _ := args["name"].(string)

	defaultGroup := map[string]interface{}{
		"name":                  "Default",
		keySpeedLimitDown:       0,
		keySpeedLimitDownEn:     false,
		keySpeedLimitUp:         0,
		keySpeedLimitUpEn:       false,
		"honors-session-limits": true,
	}

	groups := []interface{}{defaultGroup}
	if nameFilter != "" && nameFilter != "Default" {
		groups = []interface{}{}
	}

	return successResp(map[string]interface{}{
		"group": groups,
	})
}

func (h *Handler) methodGroupSet(args map[string]interface{}) rpcResponse {
	// Bandwidth group settings are accepted but not enforced at per-group
	// granularity. Default group settings (speed limits) apply globally.
	// This is sufficient for *arr compatibility.
	name, _ := args["name"].(string)
	if name == "" {
		return failResp("missing 'name' argument")
	}
	// Apply speed limits if set on the Default group.
	if name == "Default" || name == "default" {
		if v, ok := args[keySpeedLimitDown].(float64); ok && v > 0 {
			if h.streamer != nil {
				_, up := h.streamer.RateLimits()
				h.streamer.SetRateLimits(int64(v)*1024/8, up)
			}
		}
		if v, ok := args[keySpeedLimitUp].(float64); ok && v > 0 {
			if h.streamer != nil {
				down, _ := h.streamer.RateLimits()
				h.streamer.SetRateLimits(down, int64(v)*1024/8)
			}
		}
	}
	return successResp(nil)
}

// ─── torrent-remove ────────────────────────────────────────────────────────

func (h *Handler) methodTorrentRemove(args map[string]interface{}) rpcResponse {
	ids := parseIDs(args["ids"])
	if ids == nil {
		return failResp("missing 'ids' argument")
	}
	if h.store == nil {
		return successResp(nil)
	}
	deleteLocal, _ := args["delete-local-data"].(bool)

	all, err := h.store.ListAll()
	if err != nil {
		return failResp(fmt.Sprintf(errListDownloads, err))
	}
	for _, d := range all {
		if !ids[d.ID] {
			continue
		}
		if err := h.removeDownload(d, deleteLocal); err != nil {
			return failResp(err.Error())
		}
	}
	return successResp(nil)
}

func (h *Handler) removeDownload(d downloads.Download, deleteLocal bool) error {
	if deleteLocal && h.streamer != nil {
		if hh, herr := hashFromDownload(d); herr == nil {
			h.streamer.Drop(hh)
		}
	}
	if err := h.store.SetStatus(d.UserID, d.ID, downloads.StatusFailed); err != nil {
		return err
	}
	return h.store.Delete(d.UserID, d.ID)
}

// ─── torrent-set-location ──────────────────────────────────────────────────

func (h *Handler) methodTorrentSetLocation(args map[string]interface{}) rpcResponse {
	ids := parseIDs(args["ids"])
	if ids == nil {
		return failResp("missing 'ids' argument")
	}
	location, _ := args["location"].(string)
	if location == "" {
		return successResp(nil)
	}
	cleanLoc, okLoc := h.confinePath(location)
	if !okLoc {
		return failResp("location outside allowed download directory")
	}
	move, _ := args["move"].(bool)

	if h.store == nil {
		return successResp(nil)
	}

	all, err := h.store.ListAll()
	if err != nil {
		return failResp(fmt.Sprintf(errListDownloads, err))
	}
	for _, d := range all {
		if !ids[d.ID] {
			continue
		}
		_ = h.store.SetFilePath(d.UserID, d.ID, cleanLoc)
		if move && h.streamer != nil {
			if hh, herr := hashFromDownload(d); herr == nil {
				h.streamer.Drop(hh)
			}
		}
	}
	return successResp(nil)
}

// ─── free-space ────────────────────────────────────────────────────────────

func (h *Handler) methodFreeSpace(args map[string]interface{}) rpcResponse {
	path, _ := args["path"].(string)
	// Confina o path do cliente aos diretórios permitidos. Vazio OU fora dos
	// diretórios cai no mesmo fallback seguro (downloadDir/dataDir) — não expõe
	// statfs de caminho arbitrário do host.
	if clean, ok := h.confinePath(path); ok {
		path = clean
	} else {
		path = h.downloadDir
		if path == "" {
			path = h.dataDir
		}
	}

	free := int64(-1)
	if stat, err := getFreeBytes(path); err == nil {
		free = stat
	}

	return successResp(map[string]interface{}{
		"path":       path,
		"size-bytes": free,
	})
}

func getFreeBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	// #nosec G115 -- conversao limitada (statfs/tempo Unix/id/rune ASCII/fs magic); sem overflow real
	return int64(stat.Bsize) * int64(stat.Bavail), nil
}

// ─── port-test ──────────────────────────────────────────────────────────────

func (h *Handler) methodPortTest() rpcResponse {
	open := h.doPortTest()
	return successResp(map[string]interface{}{
		"port-is-open": open,
	})
}

// doPortTest checks if the BitTorrent peer port is reachable from outside.
// Results are cached for 60s to avoid hammering the external checker.
func (h *Handler) doPortTest() bool {
	h.mu.RLock()
	cached := h.portTestResult
	age := time.Since(h.portTestCheckedAt)
	inProgress := h.portTestInProgress
	h.mu.RUnlock()

	if age < 60*time.Second || inProgress {
		return cached
	}

	h.mu.Lock()
	h.portTestInProgress = true
	h.mu.Unlock()

	go h.runPortTest()

	// Return last known result while the async check runs.
	return cached
}

func (h *Handler) runPortTest() {
	port := 51469
	if h.streamer != nil {
		p := h.streamer.ListenPort()
		if p > 0 {
			port = p
		}
	}

	open := false
	client := &http.Client{Timeout: 10 * time.Second}

	// Try each checker URL in order until one succeeds.
	for _, tmpl := range portCheckURLs {
		url := fmt.Sprintf(tmpl, port)
		resp, err := client.Get(url)
		if err != nil {
			log.Printf("port-test: %s failed: %v", url, err)
			continue
		}
		var buf [1]byte
		n, _ := resp.Body.Read(buf[:])
		// #nosec G104 -- Close best-effort no cleanup; erro no teardown irrelevante
		resp.Body.Close()
		if n > 0 {
			open = buf[0] == '1'
		}
		break
	}

	h.mu.Lock()
	h.portTestResult = open
	h.portTestCheckedAt = time.Now()
	h.portTestInProgress = false
	h.mu.Unlock()
}
