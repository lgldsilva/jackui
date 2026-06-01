package transmissionrpc

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/streamer"
)

const errListDownloads = "failed to list downloads: %v"

// Constantes para literais duplicados (evita S1192 no Sonar e erros de digitação).
const (
	keyAltSpeedDown = "alt-speed-down"
	keyAltSpeedUp   = "alt-speed-up"
	keyAltSpeedEn   = "alt-speed-enabled"

	keySpeedLimitDown   = "speed-limit-down"
	keySpeedLimitUp     = "speed-limit-up"
	keySpeedLimitDownEn = "speed-limit-down-enabled"
	keySpeedLimitUpEn   = "speed-limit-up-enabled"

	keyStartAdded   = "start-added-torrents"
	keyDLQueueEn    = "download-queue-enabled"
	keyDLQueueSize  = "download-queue-size"
	keySeedQueueEn  = "seed-queue-enabled"
	keySeedQueueSize = "seed-queue-size"

	magnetPrefix = "magnet:?xt=urn:btih:"

	valUnsupportedFilename = "unsupported filename — provide a magnet URI, infoHash, or torrent URL"
)

// portCheckURLs is the list of external services used to verify if the
// BitTorrent peer port is reachable from the internet. The service must
// accept GET /<port> and respond with "1" (open) or "0" (closed).
// Multiple URLs are tried in order until one succeeds.
var portCheckURLs = []string{
	"http://portcheck.transmissionbt.com/%d",
	"https://portchecker.co/check/%d",
}

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

// JSON-RPC 2.0 request (Transmission 4.1.0+)
type jsonRPCReq struct {
	JSONRPC string                 `json:"jsonrpc"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
	ID      interface{}            `json:"id"`
}

// JSON-RPC 2.0 response
type jsonRPCResp struct {
	JSONRPC string        `json:"jsonrpc"`
	Result  interface{}   `json:"result,omitempty"`
	Error   *jsonRPCErr   `json:"error,omitempty"`
	ID      interface{}   `json:"id"`
}

type jsonRPCErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
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

	// Mutable session state (set via session-set)
	altSpeedEnabled      bool
	altSpeedDown         int
	altSpeedUp           int
	startAddedTorrents   bool
	downloadQueueEnabled bool
	downloadQueueSize    int
	seedQueueEnabled     bool
	seedQueueSize        int

	// Port test caching (port-test RPC)
	portTestResult       bool
	portTestCheckedAt    time.Time
	portTestInProgress   bool
}

func NewHandler(store *downloads.Store, s *streamer.Streamer, authStore *auth.Store, dataDir, downloadDir string) *Handler {
	return &Handler{
		store:                store,
		streamer:             s,
		authStore:            authStore,
		dataDir:              dataDir,
		downloadDir:          downloadDir,
		sessions:             make(map[string]int),
		startAddedTorrents:   true,
		downloadQueueEnabled: true,
		downloadQueueSize:    5,
		seedQueueEnabled:     false,
		seedQueueSize:        10,
		altSpeedDown:         50,
		altSpeedUp:           50,
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

// resolveSessionUser autentica o usuário via session-id ou Basic Auth.
// Retorna (userID, false) quando emite 409 (handshake necessário).
func (h *Handler) resolveSessionUser(c *gin.Context, sessionID string) (userID int, ok bool) {
	if h.authStore == nil {
		return 0, true
	}
	h.mu.RLock()
	uid, known := h.sessions[sessionID]
	h.mu.RUnlock()
	if known {
		return uid, true
	}
	sid := newSessionID()
	if user, pass, pwok := c.Request.BasicAuth(); pwok {
		if u, err := h.authStore.VerifyPassword(user, pass); err == nil && u != nil {
			userID = u.ID
			h.mu.Lock()
			h.sessions[sid] = userID
			h.mu.Unlock()
		}
	}
	h.emit409(c, sid)
	return 0, false
}

func (h *Handler) rpcHandler(c *gin.Context) {
	sessionID := c.GetHeader("X-Transmission-Session-Id")

	userID, ok := h.resolveSessionUser(c, sessionID)
	if !ok {
		return
	}

	body, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, rpcResponse{Result: "invalid request"})
		return
	}

	var probe struct {
		JSONRPC string `json:"jsonrpc"`
	}
	if json.Unmarshal(body, &probe) == nil && probe.JSONRPC == "2.0" {
		h.handleJSONRPC(c, body, userID)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, rpcResponse{Result: "invalid request"})
		return
	}

	resp := h.dispatch(req, userID)
	if req.Tag > 0 {
		resp.Tag = req.Tag
	}
	c.JSON(http.StatusOK, resp)
}

// handleJSONRPC processes a JSON-RPC 2.0 request (Transmission 4.1.0+).
func (h *Handler) handleJSONRPC(c *gin.Context, body []byte, userID int) {
	var req jsonRPCReq
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, jsonRPCResp{
			JSONRPC: "2.0",
			Error:   &jsonRPCErr{Code: -32700, Message: "Parse error"},
		})
		return
	}

	// Convert method name: session_get → session-get
	method := strings.ReplaceAll(req.Method, "_", "-")

	// Convert params keys: download_dir → download-dir (internal format)
	params := req.Params
	if params == nil {
		params = make(map[string]interface{})
	}
	params = convertMapKeys(params, snakeToKebab).(map[string]interface{})

	internalReq := rpcRequest{
		Method:    method,
		Arguments: params,
	}
	resp := h.dispatch(internalReq, userID)

	// Build JSON-RPC 2.0 response
	jsonResp := jsonRPCResp{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	if resp.Result == "success" {
		if resp.Arguments != nil {
			jsonResp.Result = convertMapKeys(resp.Arguments, toSnakeCase)
		} else {
			jsonResp.Result = map[string]interface{}{}
		}
	} else {
		jsonResp.Error = &jsonRPCErr{
			Code:    1,
			Message: resp.Result,
		}
	}

	// Notifications (no id) → HTTP 204 No Content
	if req.ID == nil {
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusOK, jsonResp)
}

// ─── Protocol conversion helpers ───────────────────────────────────────────

// convertMapKeys recursively transforms all map keys using fn.
func convertMapKeys(v interface{}, fn func(string) string) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, vv := range val {
			out[fn(k)] = convertMapKeys(vv, fn)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, item := range val {
			out[i] = convertMapKeys(item, fn)
		}
		return out
	default:
		return v
	}
}

// snakeToKebab converts download_dir → download-dir.
func snakeToKebab(s string) string {
	return strings.ReplaceAll(s, "_", "-")
}

// toSnakeCase converts any case to snake_case:
//
//	download-dir → download_dir      (kebab)
//	seedRatioLimit → seed_ratio_limit (camel)
//	rpc_version → rpc_version         (already snake)
func toSnakeCase(s string) string {
	s = strings.ReplaceAll(s, "-", "_")
	var result strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				result.WriteRune('_')
			}
			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}
	if result.Len() == 0 {
		return s
	}
	return result.String()
}

func (h *Handler) dispatch(req rpcRequest, userID int) rpcResponse {
	args := req.Arguments
	if args == nil {
		args = make(map[string]interface{})
	}

	switch req.Method {
	case "session-get":
		return h.methodSessionGet()
	case "session-set":
		return h.methodSessionSet(args)
	case "session-stats":
		return h.methodSessionStats()
	case "session-close":
		return h.methodSessionClose()
	case "torrent-add":
		return h.methodTorrentAdd(args, userID)
	case "torrent-get":
		return h.methodTorrentGet(args)
	case "torrent-set":
		return h.methodTorrentSet(args)
	case "torrent-start":
		return h.methodTorrentStart(args)
	case "torrent-stop":
		return h.methodTorrentStop(args)
	case "torrent-start-now":
		return h.methodTorrentStartNow(args)
	case "torrent-verify":
		return h.methodTorrentVerify(args)
	case "torrent-reannounce":
		return h.methodTorrentReannounce(args)
	case "torrent-remove":
		return h.methodTorrentRemove(args)
	case "torrent-set-location":
		return h.methodTorrentSetLocation(args)
	case "torrent-rename-path":
		return successResp(nil)
	case "port-test":
		return h.methodPortTest()
	case "blocklist-update":
		return successResp(map[string]interface{}{"blocklist-size": 0})
	case "free-space":
		return h.methodFreeSpace(args)
	case "group-get":
		return h.methodGroupGet(args)
	case "group-set":
		return h.methodGroupSet(args)
	case "queue-move-top":
		return h.methodQueueMove(args, "top")
	case "queue-move-up":
		return h.methodQueueMove(args, "up")
	case "queue-move-down":
		return h.methodQueueMove(args, "down")
	case "queue-move-bottom":
		return h.methodQueueMove(args, "bottom")
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

// forEachDownload applies fn to every download matching ids. When ids is nil
// (omitted), applies to ALL downloads. Returns success when no downloads match.
func (h *Handler) forEachDownload(ids map[int]bool, fn func(d downloads.Download) error) rpcResponse {
	if h.store == nil {
		return successResp(nil)
	}
	all, err := h.store.ListAll()
	if err != nil {
		return failResp(fmt.Sprintf(errListDownloads, err))
	}
	for _, d := range all {
		if ids != nil && !ids[d.ID] {
			continue
		}
		if err := fn(d); err != nil {
			log.Printf("transmission-rpc: %v", err)
		}
	}
	return successResp(nil)
}

// hashFromDownload resolves a Download's InfoHash to a metainfo.Hash.
func hashFromDownload(d downloads.Download) (metainfo.Hash, error) {
	var hh metainfo.Hash
	if err := hh.FromHexString(d.InfoHash); err != nil {
		return hh, fmt.Errorf("invalid infoHash %q: %w", d.InfoHash, err)
	}
	return hh, nil
}

// ─── session-set ───────────────────────────────────────────────────────────

func (h *Handler) methodSessionSet(args map[string]interface{}) rpcResponse {
	h.applySessionAltSpeed(args)
	h.applySessionQueue(args)
	h.applySessionSpeedLimits(args)
	return successResp(nil)
}

func (h *Handler) applySessionAltSpeed(args map[string]interface{}) {
	if v, ok := args[keyAltSpeedEn].(bool); ok {
		h.altSpeedEnabled = v
	}
	if v, ok := args[keyAltSpeedDown].(float64); ok {
		h.altSpeedDown = int(v)
	}
	if v, ok := args[keyAltSpeedUp].(float64); ok {
		h.altSpeedUp = int(v)
	}
	if v, ok := args[keyStartAdded].(bool); ok {
		h.startAddedTorrents = v
	}
}

func (h *Handler) applySessionQueue(args map[string]interface{}) {
	if v, ok := args[keyDLQueueEn].(bool); ok {
		h.downloadQueueEnabled = v
	}
	if v, ok := args[keyDLQueueSize].(float64); ok {
		h.downloadQueueSize = int(v)
	}
	if v, ok := args[keySeedQueueEn].(bool); ok {
		h.seedQueueEnabled = v
	}
	if v, ok := args[keySeedQueueSize].(float64); ok {
		h.seedQueueSize = int(v)
	}
}

func (h *Handler) applySessionSpeedLimits(args map[string]interface{}) {
	if h.streamer == nil {
		return
	}
	down := parseKbps(args, keySpeedLimitDown)
	up := parseKbps(args, keySpeedLimitUp)
	if v, ok := args[keySpeedLimitDownEn].(bool); ok && down > 0 && !v {
		down = 0
	}
	if v, ok := args[keySpeedLimitUpEn].(bool); ok && up > 0 && !v {
		up = 0
	}
	if down > 0 || up > 0 {
		h.streamer.SetRateLimits(down, up)
	} else if v, ok := args[keySpeedLimitDownEn].(bool); ok && !v {
		h.streamer.SetRateLimits(0, up)
	} else if v, ok := args[keySpeedLimitUpEn].(bool); ok && !v {
		h.streamer.SetRateLimits(down, 0)
	}
}

func parseKbps(args map[string]interface{}, key string) int64 {
	v, ok := args[key].(float64)
	if !ok {
		return 0
	}
	return int64(v) * 1024 / 8
}

// ─── session-close ─────────────────────────────────────────────────────────

func (h *Handler) methodSessionClose() rpcResponse {
	// Compatibility: *arr apps rarely call session-close. We return success
	// without actually shutting down, since JackUI serves multiple purposes
	// beyond the RPC compat layer.
	return successResp(nil)
}

// ─── torrent-start / stop / start-now ──────────────────────────────────────

func (h *Handler) methodTorrentStart(args map[string]interface{}) rpcResponse {
	return h.forEachDownload(parseIDs(args["ids"]), func(d downloads.Download) error {
		if d.Status == downloads.StatusCompleted || d.Status == downloads.StatusFailed {
			return nil
		}
		_ = h.store.SetStatus(d.UserID, d.ID, downloads.StatusDownloading)
		if h.streamer == nil {
			return nil
		}
		hh, err := hashFromDownload(d)
		if err != nil {
			return nil
		}
		_ = h.streamer.Resume(hh)
		return nil
	})
}

func (h *Handler) methodTorrentStop(args map[string]interface{}) rpcResponse {
	return h.forEachDownload(parseIDs(args["ids"]), func(d downloads.Download) error {
		if d.Status == downloads.StatusCompleted || d.Status == downloads.StatusFailed {
			return nil
		}
		_ = h.store.SetStatus(d.UserID, d.ID, downloads.StatusPaused)
		if h.streamer == nil {
			return nil
		}
		hh, err := hashFromDownload(d)
		if err != nil {
			return nil
		}
		_ = h.streamer.Pause(hh)
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
		// RecheckFile runs in a goroutine and forces a full re-verification.
		_ = h.streamer.RecheckFile(hh, d.FileIndex)
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
		"name":                   "Default",
		keySpeedLimitDown:        0,
		keySpeedLimitDownEn:      false,
		keySpeedLimitUp:          0,
		keySpeedLimitUpEn:        false,
		"honors-session-limits":  true,
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
		"start-added-torrents":             h.startAddedTorrents,
		"alt-speed-enabled":                h.altSpeedEnabled,
		"alt-speed-down":                   h.altSpeedDown,
		"alt-speed-up":                     h.altSpeedUp,
		"alt-speed-time-begin":             540,
		"alt-speed-time-day":               127,
		"alt-speed-time-enabled":           false,
		"alt-speed-time-end":               1020,
		keySpeedLimitDown:        0,
		keySpeedLimitDownEn:      false,
		keySpeedLimitUp:          0,
		keySpeedLimitUpEn:        false,
		"download-queue-enabled":           h.downloadQueueEnabled,
		"download-queue-size":              h.downloadQueueSize,
		"seed-queue-enabled":               h.seedQueueEnabled,
		"seed-queue-size":                  h.seedQueueSize,
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

type torrentAddArgs struct {
	filename           string
	metainfoB64        string
	downloadDir        string
	paused             bool
	peerLimit          float64
	bandwidthPriority  float64
}

func (h *Handler) methodTorrentAdd(args map[string]interface{}, userID int) rpcResponse {
	ta := parseTorrentAddArgs(args)

	if ta.filename == "" && ta.metainfoB64 == "" {
		return failResp("missing 'filename' or 'metainfo' argument")
	}

	var infoHash, magnet, name string

	if ta.metainfoB64 != "" {
		var err error
		infoHash, name, magnet, err = h.addTorrentMetainfo(ta.metainfoB64)
		if err != nil {
			return failResp(err.Error())
		}
	} else {
		var err error
		infoHash, magnet, err = h.addTorrentFilename(ta.filename)
		if err != nil {
			return failResp(err.Error())
		}
	}

	return h.finalizeTorrentAdd(userID, infoHash, name, magnet, ta)
}

func parseTorrentAddArgs(args map[string]interface{}) torrentAddArgs {
	return torrentAddArgs{
		filename:          argString(args, "filename"),
		metainfoB64:       argString(args, "metainfo"),
		downloadDir:       argString(args, "download-dir"),
		paused:            argBool(args, "paused"),
		peerLimit:         argFloat(args, "peer-limit"),
		bandwidthPriority: argFloat(args, "bandwidth-priority"),
	}
}

func argString(args map[string]interface{}, key string) string {
	s, _ := args[key].(string)
	return s
}

func argBool(args map[string]interface{}, key string) bool {
	b, _ := args[key].(bool)
	return b
}

func argFloat(args map[string]interface{}, key string) float64 {
	f, _ := args[key].(float64)
	return f
}

func (h *Handler) resolveCategory(downloadDir string, args map[string]interface{}) string {
	category := extractCategory(downloadDir)
	if labels, ok := args["labels"].([]interface{}); ok && len(labels) > 0 {
		if first, ok := labels[0].(string); ok && first != "" {
			return first
		}
	}
	return category
}

func (h *Handler) finalizeTorrentAdd(userID int, infoHash, name, magnet string, ta torrentAddArgs) rpcResponse {
	category := h.resolveCategory(ta.downloadDir, nil)
	shortHash := infoHash
	if len(shortHash) > 8 {
		shortHash = shortHash[:8]
	}
	if name == "" {
		name = shortHash + "..."
	}
	d, err := h.store.Create(downloads.Download{
		UserID: userID, InfoHash: infoHash, FileIndex: -1,
		Name: name, Magnet: magnet, Category: category,
	})
	if err != nil {
		return failResp(fmt.Sprintf("failed to create download: %v", err))
	}
	if ta.paused {
		_ = h.store.SetStatus(d.UserID, d.ID, downloads.StatusPaused)
	}
	h.applyAddPeerLimit(*d, ta.peerLimit)
	h.applyAddPriority(*d, ta.bandwidthPriority)
	return successResp(map[string]interface{}{
		"torrent-added": map[string]interface{}{
			"id": d.ID, "hashString": infoHash, "name": d.Name, "downloadDir": ta.downloadDir,
		},
	})
}

// addTorrentMetainfo processa um .torrent em base64 e retorna infoHash, nome, magnet.
func (h *Handler) addTorrentMetainfo(b64 string) (infoHash, name, magnet string, err error) {
	data, decodeErr := base64.StdEncoding.DecodeString(b64)
	if decodeErr != nil {
		return "", "", "", fmt.Errorf("invalid base64 metainfo")
	}
	if h.streamer != nil {
		hash, n, ierr := h.streamer.ImportTorrentBytes(data)
		if ierr != nil {
			return "", "", "", fmt.Errorf("failed to parse metainfo: %v", ierr)
		}
		infoHash = hash
		name = n
	} else {
		mi, loadErr := metainfo.Load(bytes.NewReader(data))
		if loadErr != nil {
			return "", "", "", fmt.Errorf("invalid torrent metainfo")
		}
		infoHash = mi.HashInfoBytes().HexString()
	}
	magnet = magnetPrefix + infoHash
	return infoHash, name, magnet, nil
}

// addTorrentFilename processa um filename (magnet, URL, ou infoHash) e retorna infoHash, magnet.
func (h *Handler) addTorrentFilename(filename string) (infoHash, magnet string, err error) {
	magnet = filename
	infoHash = extractInfoHash(filename)
	if infoHash == "" {
		lower := strings.ToLower(filename)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			hash, fetchErr := fetchTorrentHash(filename)
			if fetchErr != nil {
				return "", "", fmt.Errorf("failed to fetch torrent: %v", fetchErr)
			}
			infoHash = hash
		} else {
			return "", "", fmt.Errorf(valUnsupportedFilename)
		}
	} else if !strings.HasPrefix(strings.ToLower(filename), "magnet:") {
		magnet = magnetPrefix + infoHash
	}
	return infoHash, magnet, nil
}

// applyAddPeerLimit aplica peer-limit ao torrent se o streamer estiver ativo.
func (h *Handler) applyAddPeerLimit(d downloads.Download, limit float64) {
	if limit <= 0 || h.streamer == nil {
		return
	}
	if hh, err := hashFromDownload(d); err == nil {
		if t, ok := h.streamer.Client().Torrent(hh); ok {
			t.SetMaxEstablishedConns(int(limit))
		}
	}
}

// applyAddPriority aplica bandwidth-priority ao torrent se o streamer estiver ativo.
func (h *Handler) applyAddPriority(d downloads.Download, priority float64) {
	if priority == 0 || h.streamer == nil {
		return
	}
	if hh, err := hashFromDownload(d); err == nil {
		label := "normal"
		switch int(priority) {
		case -1:
			label = "low"
		case 1:
			label = "high"
		}
		_ = h.streamer.SetPriority(hh, label)
	}
}

// blockInternalIP recusa conexões a IPs internos/loopback/link-local. Roda no
// Control do dialer, DEPOIS do DNS resolver, então também barra DNS-rebinding
// (um host que resolve p/ 127.0.0.1 / 169.254.169.254 / 10.x etc.).
func blockInternalIP(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("acesso bloqueado a IP interno: %s", host)
	}
	return nil
}

// fetchTorrentHash downloads a .torrent file from a URL and returns its
// infoHash. Uses a short timeout (30s) so the RPC handler doesn't block long.
// Bloqueia IPs internos (SSRF): a URL vem do cliente RPC (*arr).
func fetchTorrentHash(url string) (string, error) {
	lower := strings.ToLower(url)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return "", fmt.Errorf("esquema de URL não suportado")
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 10 * time.Second, Control: blockInternalIP}).DialContext,
		},
	}
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
		return infoHashFromMagnet(s)
	}
	if len(s) == 40 {
		return validHexHash(s)
	}
	return ""
}

func infoHashFromMagnet(s string) string {
	query := s
	if idx := strings.Index(s, "?"); idx >= 0 {
		query = s[idx+1:]
	}
	for _, p := range strings.Split(query, "&") {
		if strings.HasPrefix(strings.ToLower(p), "xt=urn:btih:") {
			return strings.ToLower(strings.TrimPrefix(p, "xt=urn:btih:"))
		}
	}
	return ""
}

// validHexHash returns the lowercased hash if it's 40 hex chars, else "".
func validHexHash(s string) string {
	lower := strings.ToLower(s)
	for _, c := range lower {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return ""
		}
	}
	return lower
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
		return failResp(fmt.Sprintf(errListDownloads, err))
	}

	activeHashes := h.activeTorrentInfo(all)
	activeTorrentObjs := h.activeTorrentObjects(all)

	torrents := make([]interface{}, 0, len(all))
	for _, d := range all {
		if idFilter != nil && !idFilter[d.ID] {
			continue
		}
		si := activeHashes[d.InfoHash]
		to := activeTorrentObjs[d.InfoHash]
		t := h.buildTorrent(d, si, to, fieldSet)
		torrents = append(torrents, t)
	}

	return successResp(map[string]interface{}{"torrents": torrents})
}

// activeTorrentInfo resolve, por infoHash, os torrents ativos no streamer.
func (h *Handler) activeTorrentInfo(all []downloads.Download) map[string]*streamer.TorrentInfo {
	active := make(map[string]*streamer.TorrentInfo)
	if h.streamer == nil {
		return active
	}
	for _, d := range all {
		var hh metainfo.Hash
		if err := hh.FromHexString(d.InfoHash); err != nil {
			continue
		}
		if info, err := h.streamer.Get(hh); err == nil && info != nil {
			active[d.InfoHash] = info
		}
	}
	return active
}

var defaultTorrentFields = []string{
	"id", "hashString", "name", "status", "totalSize",
	"percentDone", "rateDownload", "rateUpload", "downloadDir",
	"addedDate", "doneDate", "error", "errorString",
	"leftUntilDone", "haveValid", "peersConnected",
	"eta", "isFinished", "isStalled", "labels", "trackers",
	"uploadRatio", "uploadedEver", "downloadedEver",
	"files", "fileStats", "fileCount",
	"magnetLink", "metadataPercentComplete", "trackerStats",
	"pieceCount", "pieceSize", "priorities", "wanted",
	"secondsDownloading", "secondsSeeding",
	"activityDate", "startDate", "sizeWhenDone",
	"recheckProgress", "bandwidthPriority",
	"comment", "creator", "dateCreated",
	"editDate", "honorsSessionLimits", "isPrivate",
	"peerLimit", "primaryMimeType", "sequentialDownload",
	"trackerList", "downloadLimit", "downloadLimited",
	"uploadLimit", "uploadLimited",
	"seedRatioLimit", "seedRatioMode",
	"seedIdleLimit", "seedIdleMode",
	"maxConnectedPeers", "peers",
}

// activeTorrentObjects resolve, por infoHash, os objetos *torrent.Torrent ativos.
func (h *Handler) activeTorrentObjects(all []downloads.Download) map[string]*torrent.Torrent {
	active := make(map[string]*torrent.Torrent)
	if h.streamer == nil {
		return active
	}
	client := h.streamer.Client()
	if client == nil {
		return active
	}
	for _, d := range all {
		var hh metainfo.Hash
		if err := hh.FromHexString(d.InfoHash); err != nil {
			continue
		}
		if t, ok := client.Torrent(hh); ok {
			active[d.InfoHash] = t
		}
	}
	return active
}

// torrentView agrega os valores derivados de um download (+ info do streamer)
// usados pra montar os campos do protocolo Transmission.
type torrentView struct {
	d                  downloads.Download
	trStatus           int
	downRate, upRate   int64
	peers              int
	seeders            int
	totalSize          int64
	labels             []string
	trackers           []interface{}
	trackerStats       []interface{}
	startTime          int64
	doneTime           int64
	addTime            int64
	editTime           int64
	downloadDir        string
	secondsDownloading int64
	secondsSeeding     int64
	files              []streamer.FileInfo
	trackerList        string
	magnetLink         string
	isPrivate          bool
	metadataComplete   float64
	primaryMimeType    string
	peerLimit          int
	sequentialDownload bool
	uploadedBytes      int64
	downloadedBytes    int64

	// Torrent object from anacrolix (may be nil when not active).
	// Used for pieces, peers, trackerStats that need deeper access.
	torrentObj *torrent.Torrent
}

func (h *Handler) newTorrentView(d downloads.Download, si *streamer.TorrentInfo, to *torrent.Torrent) torrentView {
	v := torrentView{
		d:           d,
		trStatus:    mapJackUIStatusToTR(d, si),
		addTime:     d.CreatedAt.Unix(),
		downloadDir: h.downloadDir,
		isPrivate:   false,
		peerLimit:   50,
		torrentObj:  to,
	}
	if si != nil {
		v.downRate = si.DownRate
		v.upRate = si.UpRate
		v.peers = si.Peers
		v.seeders = si.Seeders
		v.totalSize = si.TotalSize
		v.files = si.Files
		v.metadataComplete = 1.0
		if si.Status == "paused" {
			v.sequentialDownload = true
		}
	}
	if v.totalSize <= 0 {
		v.totalSize = d.FileSize
	}
	if v.downloadDir == "" {
		v.downloadDir = h.dataDir
	}
	if v.files == nil {
		v.files = make([]streamer.FileInfo, 0)
	}

	if to != nil {
		stats := to.Stats()
		v.uploadedBytes = stats.BytesWrittenData.Int64()
		v.downloadedBytes = stats.BytesReadData.Int64()
	}

	// Build magnet link from existing magnet or infoHash.
	if d.Magnet != "" {
		v.magnetLink = d.Magnet
	} else if d.InfoHash != "" {
		v.magnetLink = magnetPrefix + d.InfoHash
	}

	v.labels = buildLabels(d)
	v.trackers, v.trackerStats, v.trackerList = buildTrackers(d, si)

	if d.StartedAt != nil {
		v.startTime = d.StartedAt.Unix()
	}
	if d.CompletedAt != nil {
		v.doneTime = d.CompletedAt.Unix()
	}
	v.editTime = v.doneTime
	if v.editTime == 0 {
		v.editTime = v.startTime
	}
	if v.editTime == 0 {
		v.editTime = v.addTime
	}

	v.secondsDownloading, v.secondsSeeding = elapsedSeconds(d)
	return v
}

// trackerHost extracts the hostname from a tracker announce URL.
func trackerHost(announce string) string {
	if idx := strings.Index(announce, "://"); idx >= 0 {
		rest := announce[idx+3:]
		if idx2 := strings.IndexAny(rest, "/:"); idx2 >= 0 {
			return rest[:idx2]
		}
		return rest
	}
	return announce
}

// elapsedSeconds estima quanto tempo o download passou baixando e semeando.
func elapsedSeconds(d downloads.Download) (downloading, seeding int64) {
	now := time.Now().Unix()
	if d.Status == downloads.StatusCompleted && d.CompletedAt != nil {
		seeding = now - d.CompletedAt.Unix()
		if d.StartedAt != nil {
			downloading = int64(d.CompletedAt.Sub(*d.StartedAt).Seconds())
		}
	} else if d.StartedAt != nil {
		downloading = now - d.StartedAt.Unix()
	}
	if downloading < 0 {
		downloading = 0
	}
	if seeding < 0 {
		seeding = 0
	}
	return downloading, seeding
}

func (h *Handler) buildTorrent(d downloads.Download, si *streamer.TorrentInfo, to *torrent.Torrent, fields map[string]bool) map[string]interface{} {
	v := h.newTorrentView(d, si, to)
	t := make(map[string]interface{})
	for field := range fields {
		if coreTorrentField(t, field, v) {
			continue
		}
		if aggTorrentField(t, field, v) {
			continue
		}
		extraTorrentField(t, field, v)
	}
	return t
}

func computeETA(v torrentView) int {
	if v.downRate > 0 && v.totalSize > 0 {
		remaining := (v.totalSize - v.d.BytesDownloaded) / v.downRate
		return int(remaining)
	}
	return -1
}

func computeETAIdle(v torrentView) int {
	if v.upRate > 0 {
		return 0
	}
	return -1
}

func isStalled(d downloads.Download, v torrentView) bool {
	return d.Status == downloads.StatusDownloading && v.downRate == 0 && v.totalSize > 0 && v.d.BytesDownloaded < v.totalSize
}

func computeRatio(v torrentView) float64 {
	if v.downloadedBytes > 0 {
		return float64(v.uploadedBytes) / float64(v.downloadedBytes)
	}
	return 0.0
}

func queuePos(d downloads.Download) int {
	if d.Status == downloads.StatusCompleted || d.Status == downloads.StatusFailed {
		return 0
	}
	return d.ID
}

// coreTorrentField popula os campos mais comuns (defaultTorrentFields) —
// campos escalares simples sem iteração ou agregação.
func coreTorrentField(t map[string]interface{}, field string, v torrentView) bool {
	d := v.d
	switch field {
	case "id":
		t["id"] = d.ID
	case "hashString":
		t["hashString"] = d.InfoHash
	case "name":
		t["name"] = d.Name
	case "status":
		t["status"] = v.trStatus
	case "totalSize":
		t["totalSize"] = v.totalSize
	case "percentDone":
		t["percentDone"] = d.Progress
	case "rateDownload":
		t["rateDownload"] = v.downRate
	case "rateUpload":
		t["rateUpload"] = v.upRate
	case "downloadDir":
		t["downloadDir"] = v.downloadDir
	case "addedDate":
		t["addedDate"] = v.addTime
	case "doneDate":
		t["doneDate"] = v.doneTime
	case "error":
		errCode := 0
		if d.Status == downloads.StatusFailed && d.Error != "" {
			errCode = 1
		}
		t["error"] = errCode
	case "errorString":
		t["errorString"] = d.Error
	case "leftUntilDone":
		left := v.totalSize - d.BytesDownloaded
		if left < 0 {
			left = 0
		}
		t["leftUntilDone"] = left
	case "haveValid":
		t["haveValid"] = d.BytesDownloaded
	case "peersConnected":
		t["peersConnected"] = v.peers
	case "eta":
		t["eta"] = computeETA(v)
	case "etaIdle":
		t["etaIdle"] = computeETAIdle(v)
	case "isFinished":
		t["isFinished"] = d.Status == downloads.StatusCompleted
	case "isStalled":
		t["isStalled"] = isStalled(d, v)
	case "labels":
		t["labels"] = v.labels
	case "trackers":
		t["trackers"] = v.trackers
	case "uploadRatio":
		t["uploadRatio"] = computeRatio(v)
	case "uploadedEver":
		t["uploadedEver"] = v.uploadedBytes
	case "downloadedEver":
		t["downloadedEver"] = v.downloadedBytes
	case "queuePosition":
		t["queuePosition"] = queuePos(d)
	default:
		return false
	}
	return true
}

// aggTorrentField popula campos que exigem agregação ou chamada de build*().
// Separado de coreTorrentField para manter cada switch <30 branches (S1479).
func aggTorrentField(t map[string]interface{}, field string, v torrentView) bool {
	d := v.d
	switch field {
	case "magnetLink":
		t["magnetLink"] = v.magnetLink
	case "metadataPercentComplete":
		t["metadataPercentComplete"] = v.metadataComplete
	case "editDate":
		t["editDate"] = v.editTime
	case "fileCount":
		t["fileCount"] = len(v.files)
	case "percentComplete":
		t["percentComplete"] = d.Progress
	case "peers":
		t["peers"] = buildPeers(v)
	case "trackerList":
		t["trackerList"] = v.trackerList
	case "trackerStats":
		t["trackerStats"] = v.trackerStats
	case "files":
		t["files"] = buildFiles(v)
	case "fileStats":
		t["fileStats"] = buildFileStats(v)
	case "priorities":
		t["priorities"] = buildPriorities(v)
	case "wanted":
		t["wanted"] = buildWanted(v)
	case "pieces":
		t["pieces"] = buildPieces(v)
	case "availability":
		t["availability"] = []interface{}{}
	default:
		return false
	}
	return true
}

func buildPeers(v torrentView) []interface{} {
	if v.torrentObj != nil {
		swarm := v.torrentObj.KnownSwarm()
		peers := make([]interface{}, 0, len(swarm))
		for _, p := range swarm {
			addr := ""
			if p.Addr != nil {
				addr = p.Addr.String()
			}
			peers = append(peers, map[string]interface{}{
				"address":             addr,
				"clientName":          "",
				"clientIsChoked":      true,
				"clientIsInterested":  false,
				"isDownloadingFrom":   false,
				"isEncrypted":         p.SupportsEncryption,
				"isIncoming":          false,
				"isUploadingTo":       false,
				"isUTP":               false,
				"peerId":              "",
				"peerIsChoked":        true,
				"peerIsInterested":    false,
				"port":                0,
				"progress":            0.0,
				"rateToClient":        0,
				"rateToPeer":          0,
			})
		}
		return peers
	}
	return []interface{}{}
}

func buildFiles(v torrentView) []interface{} {
	if len(v.files) == 0 {
		return []interface{}{
			map[string]interface{}{
				"begin_piece":    0,
				"bytesCompleted": v.d.BytesDownloaded,
				"end_piece":      1,
				"length":         v.totalSize,
				"name":           v.d.Name,
			},
		}
	}
	files := make([]interface{}, 0, len(v.files))
	for _, f := range v.files {
		files = append(files, map[string]interface{}{
			"begin_piece":    0,
			"bytesCompleted": f.Downloaded,
			"end_piece":      0,
			"length":         f.Size,
			"name":           f.Path,
		})
	}
	return files
}

func buildFileStats(v torrentView) []interface{} {
	if len(v.files) == 0 {
		return []interface{}{
			map[string]interface{}{
				"bytesCompleted": v.d.BytesDownloaded,
				"priority":       0,
				"wanted":         true,
			},
		}
	}
	stats := make([]interface{}, 0, len(v.files))
	for _, f := range v.files {
		priority := 0
		if f.Priority == "high" {
			priority = 1
		} else if f.Priority == "low" {
			priority = -1
		}
		stats = append(stats, map[string]interface{}{
			"bytesCompleted": f.Downloaded,
			"priority":       priority,
			"wanted":         f.Progress > 0 || f.Downloaded > 0,
		})
	}
	return stats
}

func buildPriorities(v torrentView) []interface{} {
	if len(v.files) == 0 {
		return []interface{}{0}
	}
	prios := make([]interface{}, 0, len(v.files))
	for _, f := range v.files {
		p := 0
		if f.Priority == "high" {
			p = 1
		} else if f.Priority == "low" {
			p = -1
		}
		prios = append(prios, p)
	}
	return prios
}

func buildWanted(v torrentView) []interface{} {
	if len(v.files) == 0 {
		return []interface{}{1}
	}
	wanted := make([]interface{}, 0, len(v.files))
	for _, f := range v.files {
		w := 1
		if f.Priority == "off" || (f.Size > 0 && f.Progress == 0 && f.Downloaded == 0) {
			w = 0
		}
		wanted = append(wanted, w)
	}
	return wanted
}

// buildPieces returns a base64-encoded bitfield of completed pieces.
func buildPieces(v torrentView) string {
	if v.torrentObj == nil {
		return ""
	}
	info := v.torrentObj.Info()
	if info == nil {
		return ""
	}
	numPieces := int(v.torrentObj.NumPieces())
	if numPieces == 0 {
		return ""
	}
	bits := make([]byte, (numPieces+7)/8)
	for i := 0; i < numPieces; i++ {
		ps := v.torrentObj.PieceState(i)
		if ps.Complete {
			bits[i/8] |= 1 << uint(i%8)
		}
	}
	return base64.StdEncoding.EncodeToString(bits)
}

// extraTorrentField popula os campos opcionais/menos usados do protocolo.
// Os campos mais comuns são tratados em coreTorrentField.
func extraTorrentField(t map[string]interface{}, field string, v torrentView) {
	d := v.d
	switch field {
	case "activityDate":
		t["activityDate"] = v.startTime
	case "corruptEver":
		t["corruptEver"] = 0
	case "desiredAvailable":
		t["desiredAvailable"] = d.BytesDownloaded
	case "haveUnchecked":
		t["haveUnchecked"] = 0
	case "peersGettingFromUs":
		t["peersGettingFromUs"] = 0
	case "peersSendingToUs":
		t["peersSendingToUs"] = v.peers
	case "peersFrom":
		t["peersFrom"] = map[string]interface{}{
			"fromCache": 0, "fromDht": 0, "fromIncoming": 0,
			"fromLpd": 0, "fromLtep": 0, "fromPex": 0, "fromTracker": 0,
		}
	case "seedRatioLimit":
		t["seedRatioLimit"] = 2.0
	case "seedRatioMode":
		t["seedRatioMode"] = 0
	case "seedIdleLimit":
		t["seedIdleLimit"] = 0
	case "seedIdleMode":
		t["seedIdleMode"] = 0
	case "sizeWhenDone":
		t["sizeWhenDone"] = v.totalSize
	case "startDate":
		t["startDate"] = v.startTime
	case "torrentFile":
		t["torrentFile"] = ""
	case "maxConnectedPeers":
		t["maxConnectedPeers"] = v.peerLimit
	case "bandwidthPriority":
		t["bandwidthPriority"] = 0
	case "recheckProgress":
		t["recheckProgress"] = 0.0
	case "secondsDownloading":
		t["secondsDownloading"] = v.secondsDownloading
	case "secondsSeeding":
		t["secondsSeeding"] = v.secondsSeeding
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
	default:
		extraTorrentFieldSettings(t, field, v)
	}
}

// extraTorrentFieldSettings trata campos de configuração/limites — separado
// de extraTorrentField para manter cada switch <30 branches (S1479).
func extraTorrentFieldSettings(t map[string]interface{}, field string, v torrentView) {
	switch field {
	case "honorsSessionLimits":
		t["honorsSessionLimits"] = true
	case "isPrivate":
		t["isPrivate"] = v.isPrivate
	case "peerLimit":
		t["peerLimit"] = v.peerLimit
	case "primaryMimeType":
		t["primaryMimeType"] = v.primaryMimeType
	case "sequentialDownload":
		t["sequentialDownload"] = v.sequentialDownload
	case "downloadLimit":
		t["downloadLimit"] = 0
	case "downloadLimited":
		t["downloadLimited"] = false
	case "uploadLimit":
		t["uploadLimit"] = 0
	case "uploadLimited":
		t["uploadLimited"] = false
	case "bytesCompleted":
		bs := make([]int64, len(v.files))
		for i, f := range v.files {
			bs[i] = f.Downloaded
		}
		t["bytesCompleted"] = bs
	case "webseeds":
		t["webseeds"] = []string{}
	case "webseedsSendingToUs":
		t["webseedsSendingToUs"] = 0
	case "group":
		t["group"] = ""
	case "manualAnnounceTime":
		t["manualAnnounceTime"] = 0
	}
}

// buildLabels constrói a lista de labels a partir da categoria e tracker.
func buildLabels(d downloads.Download) []string {
	labels := make([]string, 0)
	if d.Category != "" {
		labels = append(labels, d.Category)
	}
	if d.Tracker != "" && d.Tracker != d.Category {
		labels = append(labels, d.Tracker)
	}
	return labels
}

// buildTrackers constrói trackers, trackerStats e trackerList para um download.
func buildTrackers(d downloads.Download, si *streamer.TorrentInfo) (trackers, trackerStats []interface{}, trackerList string) {
	trackers = make([]interface{}, 0)
	trackerStats = make([]interface{}, 0)
	if d.Tracker == "" {
		return
	}
	trackerList = d.Tracker
	trackerURLs := []string{d.Tracker}
	if si != nil && len(si.Trackers) > 0 {
		trackerURLs = si.Trackers
	}
	trackerList = strings.Join(trackerURLs, "\n\n")
	for i, tr := range trackerURLs {
		trackers = append(trackers, map[string]interface{}{
			"announce": tr, "id": i, "scrape": "", "sitename": "", "tier": 0,
		})
		trackerStats = append(trackerStats, map[string]interface{}{
			"announce": tr, "announceState": 0, "downloadCount": 0,
			"downloaderCount": 0, "hasAnnounced": false, "hasScraped": false,
			"host": trackerHost(tr), "id": i, "isBackup": false,
			"lastAnnouncePeerCount": 0, "lastAnnounceResult": "",
			"lastAnnounceStartTime": 0, "lastAnnounceSucceeded": false,
			"lastAnnounceTime": 0, "lastAnnounceTimedOut": false,
			"lastScrapeResult": "", "lastScrapeStartTime": 0,
			"lastScrapeSucceeded": false, "lastScrapeTime": 0,
			"lastScrapeTimedOut": false, "leecherCount": 0,
			"nextAnnounceTime": 0, "nextScrapeTime": 0,
			"scrape": "", "scrapeState": 0, "seederCount": 0,
			"sitename": "", "tier": 0,
		})
	}
	return
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
		return failResp(fmt.Sprintf(errListDownloads, err))
	}

	for _, d := range all {
		if ids[d.ID] {
			h.applyAllTorrentSetArgs(d, args)
		}
	}

	return successResp(nil)
}

func (h *Handler) applyAllTorrentSetArgs(d downloads.Download, args map[string]interface{}) {
	h.applyPausedArg(d, args["paused"])
	h.applyLabelsArg(d, args["labels"])
	h.applyBandwidthPriority(d, args["bandwidthPriority"])
	h.applySequentialDownload(d, args["sequentialDownload"])
	h.applyPeerLimit(d, args["peerLimit"])
	h.applyTrackerList(d, args["trackerList"])
	h.applyTrackerAdd(d, args["trackerAdd"])
	h.applyTrackerRemove(d, args["trackerRemove"])
	h.applyTrackerReplace(d, args["trackerReplace"])
	h.applySpeedLimits(d, args["downloadLimit"], args["downloadLimited"], args["uploadLimit"], args["uploadLimited"])
	h.applySeedRatio(d, args["seedRatioLimit"])
	h.applySeedIdle(d, args["seedIdleLimit"])
	h.applyQueuePosition(d, args["queuePosition"])
	h.applyHonorsSessionLimits(d, args["honorsSessionLimits"])
}

func (h *Handler) applyPausedArg(d downloads.Download, raw interface{}) {
	b, ok := raw.(bool)
	if !ok {
		return
	}
	if b {
		_ = h.store.SetStatus(d.UserID, d.ID, downloads.StatusPaused)
	} else if d.Status == downloads.StatusPaused {
		_ = h.store.SetStatus(d.UserID, d.ID, downloads.StatusDownloading)
	}
}

func (h *Handler) applyLabelsArg(d downloads.Download, raw interface{}) {
	labels, ok := raw.([]interface{})
	if !ok || len(labels) == 0 {
		return
	}
	cat, ok := labels[0].(string)
	if !ok {
		return
	}
	_ = h.store.SetCategory(d.UserID, d.ID, cat)
}

func (h *Handler) applyBandwidthPriority(d downloads.Download, raw interface{}) {
	v, ok := raw.(float64)
	if !ok || h.streamer == nil {
		return
	}
	hh, err := hashFromDownload(d)
	if err != nil {
		return
	}
	// Map Transmission priority (-1 low, 0 normal, 1 high) to streamer labels.
	label := "normal"
	switch int(v) {
	case -1:
		label = "low"
	case 1:
		label = "high"
	}
	_ = h.streamer.SetPriority(hh, label)
}

func (h *Handler) applySequentialDownload(d downloads.Download, raw interface{}) {
	enable, ok := raw.(bool)
	if !ok || h.streamer == nil {
		return
	}
	hh, err := hashFromDownload(d)
	if err != nil {
		return
	}
	client := h.streamer.Client()
	if client == nil {
		return
	}
	t, ok := client.Torrent(hh)
	if !ok {
		return
	}
	if enable {
		// Sequential: download pieces in order from 0.
		// Cancel all current piece requests, then request from beginning.
		num := int(t.NumPieces())
		t.CancelPieces(0, int(num))
		t.DownloadPieces(0, int(num))
	} else {
		t.DownloadAll()
	}
}

func (h *Handler) applyPeerLimit(d downloads.Download, raw interface{}) {
	v, ok := raw.(float64)
	if !ok || h.streamer == nil {
		return
	}
	hh, err := hashFromDownload(d)
	if err != nil {
		return
	}
	client := h.streamer.Client()
	if client == nil {
		return
	}
	if t, ok := client.Torrent(hh); ok {
		t.SetMaxEstablishedConns(int(v))
	}
}

func (h *Handler) applyTrackerList(d downloads.Download, raw interface{}) {
	list, ok := raw.(string)
	if !ok || list == "" {
		return
	}
	tiers := parseTrackerTiers(list)
	if len(tiers) == 0 {
		return
	}
	h.applyTrackerTiers(d, tiers)
}

// parseTrackerTiers converte uma string multi-tier do Transmission
// (tier 1, linha vazia, tier 2, ...) em [][]string.
func parseTrackerTiers(list string) [][]string {
	var tiers [][]string
	for _, block := range strings.Split(list, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var urls []string
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				urls = append(urls, line)
			}
		}
		if len(urls) > 0 {
			tiers = append(tiers, urls)
		}
	}
	return tiers
}

func (h *Handler) applyTrackerTiers(d downloads.Download, tiers [][]string) {
	if h.streamer != nil {
		if hh, err := hashFromDownload(d); err == nil {
			if t, ok := h.streamer.Client().Torrent(hh); ok {
				t.ModifyTrackers(tiers)
			}
		}
	}
	var all []string
	for _, tier := range tiers {
		all = append(all, tier...)
	}
	if len(all) > 0 {
		_ = h.store.SetCategory(d.UserID, d.ID, all[0])
	}
}

func (h *Handler) applyTrackerAdd(d downloads.Download, raw interface{}) {
	urls, ok := raw.([]interface{})
	if !ok || len(urls) == 0 {
		return
	}
	var announceList [][]string
	for _, u := range urls {
		if s, ok := u.(string); ok && s != "" {
			announceList = append(announceList, []string{s})
		}
	}
	if len(announceList) == 0 || h.streamer == nil {
		return
	}
	if hh, err := hashFromDownload(d); err == nil {
		client := h.streamer.Client()
		if t, ok := client.Torrent(hh); ok {
			t.AddTrackers(announceList)
		}
	}
}

func (h *Handler) applyTrackerRemove(d downloads.Download, raw interface{}) {
	ids, ok := raw.([]interface{})
	if !ok || len(ids) == 0 {
		return
	}
	// trackerRemove: array of tracker IDs to remove.
	// We can't remove individual trackers via anacrolix API directly,
	// but we can read the current list and rebuild without the removed IDs.
	_ = ids
}

func (h *Handler) applyTrackerReplace(d downloads.Download, raw interface{}) {
	pairs, ok := raw.([]interface{})
	if !ok || len(pairs) == 0 {
		return
	}
	// trackerReplace: array of [trackerId, newUrl] pairs.
	var announceList [][]string
	for _, pair := range pairs {
		p, ok := pair.([]interface{})
		if !ok || len(p) < 2 {
			continue
		}
		url, ok := p[1].(string)
		if !ok || url == "" {
			continue
		}
		announceList = append(announceList, []string{url})
	}
	if len(announceList) == 0 || h.streamer == nil {
		return
	}
	if hh, err := hashFromDownload(d); err == nil {
		client := h.streamer.Client()
		if t, ok := client.Torrent(hh); ok {
			t.AddTrackers(announceList)
		}
	}
}

func (h *Handler) applySeedRatio(d downloads.Download, raw interface{}) {
	// seedRatioLimit + seedRatioMode are accepted but not enforced.
	// The download worker doesn't stop seeding by ratio.
}

func (h *Handler) applySeedIdle(d downloads.Download, raw interface{}) {
	// seedIdleLimit + seedIdleMode are accepted but not enforced.
}

func (h *Handler) applyQueuePosition(d downloads.Download, raw interface{}) {
	// Queue position is best-effort: lower queuePosition = higher priority.
	// The store doesn't have a dedicated column, so we use ID as proxy.
}

func (h *Handler) applyHonorsSessionLimits(d downloads.Download, raw interface{}) {
	// When false, the torrent ignores global speed limits.
	// Not directly supported by anacrolix; we accept but don't enforce.
}

func (h *Handler) applySpeedLimits(d downloads.Download, dlRaw, dlLimited, ulRaw, ulLimited interface{}) {
	// Per-torrent speed limits. The anacrolix library v1.61 does not support
	// per-torrent bandwidth caps. Values are accepted for *arr compatibility
	// but not enforced at torrent level — global limits apply instead.
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
		if deleteLocal && h.streamer != nil {
			if hh, herr := hashFromDownload(d); herr == nil {
				h.streamer.Drop(hh)
			}
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
	location, _ := args["location"].(string)
	if location == "" {
		return successResp(nil)
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
		_ = h.store.SetFilePath(d.UserID, d.ID, location)
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


