package transmissionrpc

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/streamer"
)

type rpcRequest struct {
	Method    string                 `json:"method"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
	Tag       int                    `json:"tag,omitempty"`
}

type rpcResponse struct {
	Result    string                 `json:"result"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
	Tag       int                    `json:"tag,omitempty"`
}

type Handler struct {
	store       *downloads.Store
	streamer    *streamer.Streamer
	authStore   *auth.Store
	dataDir     string
	downloadDir string

	mu         sync.RWMutex
	// sessionID → userID. When auth is disabled all sessions map to 0 (system).
	sessions map[string]int
}

func NewHandler(store *downloads.Store, s *streamer.Streamer, authStore *auth.Store, dataDir, downloadDir string) *Handler {
	return &Handler{
		store:       store,
		streamer:    s,
		authStore:   authStore,
		dataDir:     dataDir,
		downloadDir: downloadDir,
		sessions:    make(map[string]int),
	}
}

func (h *Handler) RegisterRoutes(router *gin.Engine) {
	router.POST("/transmission/rpc", h.rpcHandler)
}

func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "deadbeef0102030405060708090a0b0c"
	}
	return hex.EncodeToString(b)
}

func (h *Handler) emit409(c *gin.Context, sid string) {
	c.Header("X-Transmission-Session-Id", sid)
	c.Header("X-Transmission-Rpc-Version", "6.0.1")
	c.JSON(http.StatusConflict, rpcResponse{Result: "success"})
}

func (h *Handler) rpcHandler(c *gin.Context) {
	sessionID := c.GetHeader("X-Transmission-Session-Id")

	// Resolve user. When the auth store exists (JWT enabled), we validate
	// credentials and issue session IDs. Otherwise all requests are accepted
	// as system user (0).
	userID := 0

	if h.authStore != nil {
		h.mu.RLock()
		uid, known := h.sessions[sessionID]
		h.mu.RUnlock()

		if !known {
			sid := newSessionID()
			if user, pass, ok := c.Request.BasicAuth(); ok {
				if u, err := h.authStore.VerifyPassword(user, pass); err == nil && u != nil {
					userID = u.ID
					h.mu.Lock()
					h.sessions[sid] = userID
					h.mu.Unlock()
				}
			}
			h.emit409(c, sid)
			return
		}
		userID = uid
	}

	var req rpcRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, rpcResponse{Result: "invalid request"})
		return
	}

	resp := h.dispatch(req, userID)
	if req.Tag > 0 {
		resp.Tag = req.Tag
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) dispatch(req rpcRequest, userID int) rpcResponse {
	args := req.Arguments
	if args == nil {
		args = make(map[string]interface{})
	}

	switch req.Method {
	case "session-get":
		return h.methodSessionGet()
	case "session-stats":
		return h.methodSessionStats()
	case "torrent-add":
		return h.methodTorrentAdd(args, userID)
	case "torrent-get":
		return h.methodTorrentGet(args)
	case "torrent-set":
		return h.methodTorrentSet(args)
	case "torrent-remove":
		return h.methodTorrentRemove(args)
	case "torrent-set-location":
		return h.methodTorrentSetLocation(args)
	case "torrent-rename-path":
		return successResp(nil)
	case "port-test":
		return successResp(map[string]interface{}{"port-is-open": true})
	case "blocklist-update":
		return successResp(map[string]interface{}{"blocklist-size": 0})
	case "free-space":
		return h.methodFreeSpace(args)
	default:
		log.Printf("transmission-rpc: unhandled method %q", req.Method)
		return rpcResponse{Result: "no such method"}
	}
}

func successResp(args map[string]interface{}) rpcResponse {
	return rpcResponse{Result: "success", Arguments: args}
}

func failResp(msg string) rpcResponse {
	return rpcResponse{Result: msg}
}

// ─── session-get ───────────────────────────────────────────────────────────

func (h *Handler) methodSessionGet() rpcResponse {
	dir := h.downloadDir
	if dir == "" {
		dir = h.dataDir
	}
	freeSpace := int64(-1)
	if stat, err := getFreeBytes(dir); err == nil {
		freeSpace = stat
	}

	// Generate a stable session-id for this response (matches the one in
	// the header we sent on the 409, or a fresh one when auth is disabled).
	sessionID := h.generateSessionID(dir)

	return successResp(map[string]interface{}{
		"version":                          "4.1.1",
		"rpc-version":                      19,
		"rpc-version-semver":               "6.0.1",
		"rpc-version-minimum":              14,
		"session-id":                       sessionID,
		"download-dir":                     dir,
		"download-dir-free-space":          freeSpace,
		"incomplete-dir":                   h.dataDir,
		"incomplete-dir-enabled":           true,
		"config-dir":                       "/config",
		"cache-size-mb":                    4,
		"seedRatioLimit":                   2.0,
		"seedRatioLimited":                 false,
		"peer-port":                        51469,
		"peer-port-random-on-start":        false,
		"peer-limit-global":                200,
		"peer-limit-per-torrent":           50,
		"pex-enabled":                      true,
		"dht-enabled":                      true,
		"lpd-enabled":                      false,
		"utp-enabled":                      true,
		"tcp-enabled":                      true,
		"encryption":                       "preferred",
		"start-added-torrents":             true,
		"alt-speed-enabled":                false,
		"alt-speed-down":                   50,
		"alt-speed-up":                     50,
		"alt-speed-time-begin":             540,
		"alt-speed-time-day":               127,
		"alt-speed-time-enabled":           false,
		"alt-speed-time-end":               1020,
		"speed-limit-down":                 0,
		"speed-limit-down-enabled":         false,
		"speed-limit-up":                   0,
		"speed-limit-up-enabled":           false,
		"download-queue-enabled":           true,
		"download-queue-size":              5,
		"seed-queue-enabled":               false,
		"seed-queue-size":                  10,
		"queue-stalled-enabled":            true,
		"queue-stalled-minutes":            30,
		"idle-seeding-limit":               3000,
		"idle-seeding-limit-enabled":       true,
		"anti-brute-force-enabled":         false,
		"anti-brute-force-threshold":       100,
		"blocklist-enabled":                false,
		"blocklist-size":                   0,
		"blocklist-url":                    "http://www.example.com/blocklist",
		"default-trackers":                 "",
		"preferred_transports":             []string{"utp", "tcp"},
		"rename-partial-files":             true,
		"reqq":                             2000,
		"script-torrent-added-enabled":     false,
		"script-torrent-added-filename":    "",
		"script-torrent-done-enabled":      false,
		"script-torrent-done-filename":     "",
		"script-torrent-done-seeding-enabled": false,
		"script-torrent-done-seeding-filename": "",
		"sequential_download":              false,
		"trash-original-torrent-files":     false,
		"port-forwarding-enabled":          false,
		"units": map[string]interface{}{
			"memory-bytes": 1024,
			"memory-units": []string{"B", "KiB", "MiB", "GiB", "TiB"},
			"size-bytes":   1000,
			"size-units":   []string{"B", "kB", "MB", "GB", "TB"},
			"speed-bytes":  1000,
			"speed-units":  []string{"B/s", "kB/s", "MB/s", "GB/s", "TB/s"},
		},
	})
}

// generateSessionID returns a deterministic session ID for the response.
func (h *Handler) generateSessionID(dir string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for sid := range h.sessions {
		return sid
	}
	return newSessionID()
}

// ─── session-stats ─────────────────────────────────────────────────────────

func (h *Handler) methodSessionStats() rpcResponse {
	var activeCount, downloadCount, seedCount int
	var downSpeed, upSpeed int64

	if h.streamer != nil {
		stats := h.streamer.GlobalStats()
		activeCount = stats.ActiveTorrents
		downSpeed = stats.DownRate
		upSpeed = stats.UpRate
	}

	if h.store != nil {
		all, err := h.store.ListAll()
		if err == nil {
			for _, d := range all {
				switch d.Status {
				case downloads.StatusDownloading, downloads.StatusQueued:
					downloadCount++
				case downloads.StatusCompleted:
					seedCount++
				}
			}
		}
	}

	return successResp(map[string]interface{}{
		"activeTorrentCount": activeCount,
		"downloadSpeed":      downSpeed,
		"uploadSpeed":        upSpeed,
		"pausedTorrentCount": downloadCount + seedCount - activeCount,
		"torrentCount":       downloadCount + seedCount,
		"cumulative-stats": map[string]interface{}{
			"downloadedBytes": 0,
			"uploadedBytes":   0,
			"filesAdded":      0,
			"secondsActive":   0,
			"sessionCount":    1,
		},
		"current-stats": map[string]interface{}{
			"downloadedBytes": 0,
			"uploadedBytes":   0,
			"filesAdded":      0,
			"secondsActive":   0,
			"startTime":       time.Now().Unix(),
			"sessionCount":    1,
		},
	})
}

// ─── torrent-add ───────────────────────────────────────────────────────────

func (h *Handler) methodTorrentAdd(args map[string]interface{}, userID int) rpcResponse {
	filename, _ := args["filename"].(string)
	if filename == "" {
		return failResp("invalid or missing 'filename' argument")
	}
	downloadDir, _ := args["download-dir"].(string)
	paused, _ := args["paused"].(bool)

	magnet := filename
	infoHash := extractInfoHash(filename)

	if infoHash == "" {
		lower := strings.ToLower(filename)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			hash, err := fetchTorrentHash(filename)
			if err != nil {
				return failResp(fmt.Sprintf("failed to fetch torrent: %v", err))
			}
			infoHash = hash
		} else {
			return failResp("unsupported filename — provide a magnet URI, infoHash, or torrent URL")
		}
	} else if !strings.HasPrefix(strings.ToLower(filename), "magnet:") {
		magnet = "magnet:?xt=urn:btih:" + infoHash
	}

	category := extractCategory(downloadDir)

	d, err := h.store.Create(downloads.Download{
		UserID:    0,
		InfoHash:  infoHash,
		FileIndex: -1,
		Name:      infoHash[:8] + "...",
		Magnet:    magnet,
		Category:  category,
	})
	if err != nil {
		return failResp(fmt.Sprintf("failed to create download: %v", err))
	}

	if paused {
		_ = h.store.SetStatus(d.UserID, d.ID, downloads.StatusPaused)
	}

	return successResp(map[string]interface{}{
		"torrent-added": map[string]interface{}{
			"id":          d.ID,
			"hashString":  infoHash,
			"name":        d.Name,
			"downloadDir": downloadDir,
		},
	})
}

// fetchTorrentHash downloads a .torrent file from a URL and returns its
// infoHash. Uses a short timeout (30s) so the RPC handler doesn't block long.
func fetchTorrentHash(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	mi, err := metainfo.Load(resp.Body)
	if err != nil {
		return "", fmt.Errorf("parse torrent: %w", err)
	}

	hash := mi.HashInfoBytes().HexString()
	if hash == "" {
		return "", fmt.Errorf("empty infoHash from torrent")
	}
	return strings.ToLower(hash), nil
}

func extractInfoHash(s string) string {
	s = strings.TrimSpace(s)

	if strings.HasPrefix(strings.ToLower(s), "magnet:") {
		query := s
		if idx := strings.Index(s, "?"); idx >= 0 {
			query = s[idx+1:]
		}
		for _, p := range strings.Split(query, "&") {
			lower := strings.ToLower(p)
			if strings.HasPrefix(lower, "xt=urn:btih:") {
				return strings.ToLower(strings.TrimPrefix(p, "xt=urn:btih:"))
			}
		}
		return ""
	}

	if len(s) == 40 {
		lower := strings.ToLower(s)
		for _, c := range lower {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				return ""
			}
		}
		return lower
	}

	return ""
}

func extractCategory(downloadDir string) string {
	if downloadDir == "" {
		return ""
	}
	parts := strings.Split(strings.TrimRight(downloadDir, "/"), "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return parts[len(parts)-1]
}

// ─── torrent-get ───────────────────────────────────────────────────────────

func (h *Handler) methodTorrentGet(args map[string]interface{}) rpcResponse {
	rawFields, _ := args["fields"].([]interface{})
	fieldSet := make(map[string]bool, len(rawFields))
	for _, f := range rawFields {
		if s, ok := f.(string); ok {
			fieldSet[s] = true
		}
	}
	if len(fieldSet) == 0 {
		for _, f := range defaultTorrentFields {
			fieldSet[f] = true
		}
	}

	idFilter := parseIDs(args["ids"])

	if h.store == nil {
		return successResp(map[string]interface{}{"torrents": []interface{}{}})
	}

	all, err := h.store.ListAll()
	if err != nil {
		return failResp(fmt.Sprintf("failed to list downloads: %v", err))
	}

	activeHashes := make(map[string]*streamer.TorrentInfo)
	if h.streamer != nil {
		for _, d := range all {
			var hh metainfo.Hash
			if err := hh.FromHexString(d.InfoHash); err != nil {
				continue
			}
			if info, err := h.streamer.Get(hh); err == nil && info != nil {
				activeHashes[d.InfoHash] = info
			}
		}
	}

	torrents := make([]interface{}, 0, len(all))
	for _, d := range all {
		if idFilter != nil && !idFilter[d.ID] {
			continue
		}
		si := activeHashes[d.InfoHash]
		t := h.buildTorrent(d, si, fieldSet)
		torrents = append(torrents, t)
	}

	return successResp(map[string]interface{}{"torrents": torrents})
}

var defaultTorrentFields = []string{
	"id", "hashString", "name", "status", "totalSize",
	"percentDone", "rateDownload", "rateUpload", "downloadDir",
	"addedDate", "doneDate", "error", "errorString",
	"leftUntilDone", "haveValid", "peersConnected",
	"eta", "isFinished", "isStalled", "labels", "trackers",
	"uploadRatio", "uploadedEver", "downloadedEver",
}

func (h *Handler) buildTorrent(d downloads.Download, si *streamer.TorrentInfo, fields map[string]bool) map[string]interface{} {
	t := make(map[string]interface{})
	trStatus := mapJackUIStatusToTR(d, si)

	var downRate, upRate int64
	var peers int
	var totalSize int64
	if si != nil {
		downRate = si.DownRate
		upRate = si.UpRate
		peers = si.Peers
		totalSize = si.TotalSize
	}
	if totalSize <= 0 {
		totalSize = d.FileSize
	}

	labels := make([]string, 0)
	if d.Category != "" {
		labels = append(labels, d.Category)
	}
	if d.Tracker != "" && d.Tracker != d.Category {
		labels = append(labels, d.Tracker)
	}

	trackers := make([]interface{}, 0)
	if d.Tracker != "" {
		trackers = append(trackers, map[string]interface{}{
			"announce":  d.Tracker,
			"id":        0,
			"scrape":    "",
			"sitename":  "",
			"tier":      0,
		})
	}

	startTime := int64(0)
	if d.StartedAt != nil {
		startTime = d.StartedAt.Unix()
	}
	doneTime := int64(0)
	if d.CompletedAt != nil {
		doneTime = d.CompletedAt.Unix()
	}
	addTime := d.CreatedAt.Unix()

	downloadDir := h.downloadDir
	if downloadDir == "" {
		downloadDir = h.dataDir
	}

	// Estimate durations
	secondsDownloading := int64(0)
	secondsSeeding := int64(0)
	now := time.Now().Unix()
	if d.Status == downloads.StatusCompleted && d.CompletedAt != nil {
		secondsDownloading = int64(d.CompletedAt.Sub(*d.StartedAt).Seconds())
		secondsSeeding = now - d.CompletedAt.Unix()
	} else if d.StartedAt != nil {
		secondsDownloading = now - d.StartedAt.Unix()
	}
	if secondsDownloading < 0 {
		secondsDownloading = 0
	}
	if secondsSeeding < 0 {
		secondsSeeding = 0
	}

	for field := range fields {
		switch field {
		case "id":
			t["id"] = d.ID
		case "hashString":
			t["hashString"] = d.InfoHash
		case "name":
			t["name"] = d.Name
		case "status":
			t["status"] = trStatus
		case "totalSize":
			t["totalSize"] = totalSize
		case "percentDone":
			t["percentDone"] = d.Progress
		case "rateDownload":
			t["rateDownload"] = downRate
		case "rateUpload":
			t["rateUpload"] = upRate
		case "downloadDir":
			t["downloadDir"] = downloadDir
		case "addedDate":
			t["addedDate"] = addTime
		case "doneDate":
			t["doneDate"] = doneTime
		case "error":
			errCode := 0
			if d.Status == downloads.StatusFailed && d.Error != "" {
				errCode = 1
			}
			t["error"] = errCode
		case "errorString":
			t["errorString"] = d.Error
		case "leftUntilDone":
			left := totalSize - d.BytesDownloaded
			if left < 0 {
				left = 0
			}
			t["leftUntilDone"] = left
		case "haveValid":
			t["haveValid"] = d.BytesDownloaded
		case "peersConnected":
			t["peersConnected"] = peers
		case "eta":
			eta := -1
			if downRate > 0 && totalSize > 0 {
				remaining := (totalSize - d.BytesDownloaded) / downRate
				eta = int(remaining)
			}
			t["eta"] = eta
		case "isFinished":
			t["isFinished"] = d.Status == downloads.StatusCompleted
		case "isStalled":
			t["isStalled"] = d.Status == downloads.StatusDownloading && downRate == 0 && totalSize > 0 && d.BytesDownloaded < totalSize
		case "labels":
			t["labels"] = labels
		case "trackers":
			t["trackers"] = trackers
		case "uploadRatio":
			t["uploadRatio"] = 0.0
		case "uploadedEver":
			t["uploadedEver"] = 0
		case "downloadedEver":
			t["downloadedEver"] = d.BytesDownloaded
		case "queuePosition":
			if d.Status == downloads.StatusCompleted || d.Status == downloads.StatusFailed {
				t["queuePosition"] = 0
			} else {
				t["queuePosition"] = d.ID
			}
		case "activityDate":
			t["activityDate"] = startTime
		case "corruptEver":
			t["corruptEver"] = 0
		case "desiredAvailable":
			t["desiredAvailable"] = d.BytesDownloaded
		case "haveUnchecked":
			t["haveUnchecked"] = 0
		case "peersGettingFromUs":
			t["peersGettingFromUs"] = 0
		case "peersSendingToUs":
			t["peersSendingToUs"] = peers
		case "seedRatioLimit":
			t["seedRatioLimit"] = 2.0
		case "seedRatioMode":
			t["seedRatioMode"] = 0
		case "sizeWhenDone":
			t["sizeWhenDone"] = totalSize
		case "startDate":
			t["startDate"] = startTime
		case "torrentFile":
			t["torrentFile"] = ""
		case "maxConnectedPeers":
			t["maxConnectedPeers"] = 50
		case "bandwidthPriority":
			t["bandwidthPriority"] = 0
		case "recheckProgress":
			t["recheckProgress"] = 0.0
		case "secondsDownloading":
			t["secondsDownloading"] = secondsDownloading
		case "secondsSeeding":
			t["secondsSeeding"] = secondsSeeding
		case "comment":
			t["comment"] = ""
		case "creator":
			t["creator"] = ""
		case "dateCreated":
			t["dateCreated"] = 0
		case "pieceCount":
			t["pieceCount"] = 0
		case "pieceSize":
			t["pieceSize"] = 0
		case "priorities":
			t["priorities"] = []int{0}
		case "wanted":
			t["wanted"] = []int{1}
		case "files":
			t["files"] = []interface{}{
				map[string]interface{}{
					"begin_piece":     0,
					"bytesCompleted":  d.BytesDownloaded,
					"end_piece":       1,
					"length":          totalSize,
					"name":            d.Name,
				},
			}
		case "fileStats":
			t["fileStats"] = []interface{}{
				map[string]interface{}{
					"bytesCompleted": d.BytesDownloaded,
					"priority":       0,
					"wanted":         true,
				},
			}
		}
	}
	return t
}

func mapJackUIStatusToTR(d downloads.Download, si *streamer.TorrentInfo) int {
	if si != nil {
		switch si.Status {
		case "paused":
			return 0
		case "seeding":
			return 6
		case "downloading":
			if si.Progress > 0 {
				return 4
			}
			return 3
		}
	}

	switch d.Status {
	case downloads.StatusQueued:
		return 3
	case downloads.StatusDownloading:
		if d.Progress >= 1.0 {
			return 6
		}
		return 4
	case downloads.StatusCompleted:
		return 6
	case downloads.StatusPaused:
		return 0
	case downloads.StatusFailed:
		return 0
	default:
		return 0
	}
}

func parseIDs(raw interface{}) map[int]bool {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case float64:
		return map[int]bool{int(v): true}
	case []interface{}:
		set := make(map[int]bool, len(v))
		for _, e := range v {
			if n, ok := e.(float64); ok {
				set[int(n)] = true
			}
		}
		return set
	}
	return nil
}

// ─── torrent-set ───────────────────────────────────────────────────────────

func (h *Handler) methodTorrentSet(args map[string]interface{}) rpcResponse {
	ids := parseIDs(args["ids"])
	if ids == nil {
		return successResp(nil)
	}
	if h.store == nil {
		return successResp(nil)
	}

	all, err := h.store.ListAll()
	if err != nil {
		return failResp(fmt.Sprintf("failed to list downloads: %v", err))
	}

	for _, d := range all {
		if !ids[d.ID] {
			continue
		}
		if v, ok := args["seedRatioLimit"]; ok {
			_ = v
		}
		if v, ok := args["seedRatioMode"]; ok {
			_ = v
		}
		if paused, ok := args["paused"]; ok {
			if b, ok := paused.(bool); ok && b {
				_ = h.store.SetStatus(d.UserID, d.ID, downloads.StatusPaused)
			} else if ok && !b {
				if d.Status == downloads.StatusPaused {
					_ = h.store.SetStatus(d.UserID, d.ID, downloads.StatusDownloading)
				}
			}
		}
		if rawLabels, ok := args["labels"]; ok {
			if labels, ok := rawLabels.([]interface{}); ok && len(labels) > 0 {
				if cat, ok := labels[0].(string); ok {
					_, _ = h.store.Create(downloads.Download{
						UserID:   d.UserID,
						InfoHash: d.InfoHash,
						Category: cat,
					})
				}
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

	all, err := h.store.ListAll()
	if err != nil {
		return failResp(fmt.Sprintf("failed to list downloads: %v", err))
	}
	for _, d := range all {
		if !ids[d.ID] {
			continue
		}
		_ = h.store.SetStatus(d.UserID, d.ID, downloads.StatusFailed)
		_ = h.store.Delete(d.UserID, d.ID)
	}
	return successResp(nil)
}

// ─── torrent-set-location ──────────────────────────────────────────────────

func (h *Handler) methodTorrentSetLocation(args map[string]interface{}) rpcResponse {
	ids := parseIDs(args["ids"])
	if ids == nil {
		return failResp("missing 'ids' argument")
	}
	return successResp(nil)
}

// ─── free-space ────────────────────────────────────────────────────────────

func (h *Handler) methodFreeSpace(args map[string]interface{}) rpcResponse {
	path, _ := args["path"].(string)
	if path == "" {
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
	return int64(stat.Bsize) * int64(stat.Bavail), nil
}


