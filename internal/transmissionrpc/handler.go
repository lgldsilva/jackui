package transmissionrpc

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

const errListDownloads = "failed to list downloads: %v"

// Limites anti-DoS: o corpo do RPC e o .torrent baixado de uma URL são pequenos
// por natureza; capamos generosamente p/ evitar alocação sob controle do cliente.
const (
	maxRPCBodyBytes     = 16 << 20 // 16 MiB
	maxTorrentFileBytes = 16 << 20 // 16 MiB
)

// Constantes para literais duplicados (evita S1192 no Sonar e erros de digitação).
const (
	keyAltSpeedDown = "alt-speed-down"
	keyAltSpeedUp   = "alt-speed-up"
	keyAltSpeedEn   = "alt-speed-enabled"

	keySpeedLimitDown   = "speed-limit-down"
	keySpeedLimitUp     = "speed-limit-up"
	keySpeedLimitDownEn = "speed-limit-down-enabled"
	keySpeedLimitUpEn   = "speed-limit-up-enabled"

	keyStartAdded    = "start-added-torrents"
	keyDLQueueEn     = "download-queue-enabled"
	keyDLQueueSize   = "download-queue-size"
	keySeedQueueEn   = "seed-queue-enabled"
	keySeedQueueSize = "seed-queue-size"

	magnetPrefix = "magnet:?xt=urn:btih:"

	valUnsupportedFilename = "unsupported filename — provide a magnet URI, infoHash, or torrent URL"

	// headerTransmissionSessionID is the CSRF session-id header of the Transmission
	// RPC handshake (used on the 409 emit + the GET/POST session lookups).
	headerTransmissionSessionID = "X-Transmission-Session-Id"
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

// JSON-RPC 2.0 request (Transmission 4.1.0+)
type jsonRPCReq struct {
	JSONRPC string                 `json:"jsonrpc"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
	ID      interface{}            `json:"id"`
}

// JSON-RPC 2.0 response
type jsonRPCResp struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *jsonRPCErr `json:"error,omitempty"`
	ID      interface{} `json:"id"`
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
	// sharedDir + autoPromote drive the *arr auto-promote: when autoPromote() is
	// on, an *arr download's reported download-dir is sharedDir/<category> — the
	// Transmission-style completed-downloads tree where the worker actually places
	// the finished files (downloads.PromoteDir), so the *arr import from the right
	// path. nil autoPromote ⇒ feature off (reports the plain downloadDir).
	sharedDir   string
	autoPromote func() bool

	mu sync.RWMutex
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
	portTestResult     bool
	portTestCheckedAt  time.Time
	portTestInProgress bool
}

func NewHandler(store *downloads.Store, s *streamer.Streamer, authStore *auth.Store, dataDir, downloadDir, sharedDir string, autoPromote func() bool) *Handler {
	return &Handler{
		store:                store,
		streamer:             s,
		authStore:            authStore,
		dataDir:              dataDir,
		downloadDir:          downloadDir,
		sharedDir:            sharedDir,
		autoPromote:          autoPromote,
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
	// *arr clients (Sonarr/Radarr/Prowlarr) fetch the CSRF session-id with a GET
	// to /transmission/rpc BEFORE POSTing — the real Transmission daemon answers
	// any session-less request with 409 + X-Transmission-Session-Id. Without this
	// the GET fell through to the SPA (200 HTML, no session header) and the client
	// reported "Authentication failure". Mirror Transmission's handshake on GET.
	router.GET("/transmission/rpc", h.rpcSessionProbe)
}

// confinePath limpa um caminho vindo do cliente RPC e exige que ele esteja
// dentro de um dos diretórios permitidos (downloadDir/dataDir). Sem isso, um
// cliente poderia apontar set-location/free-space pra fora da árvore de download
// (path traversal) e influenciar quais arquivos o streamer abre depois.
func (h *Handler) confinePath(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	abs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return "", false
	}
	for _, root := range []string{h.downloadDir, h.dataDir} {
		if root == "" {
			continue
		}
		rabs, rerr := filepath.Abs(root)
		if rerr != nil {
			continue
		}
		if abs == rabs || strings.HasPrefix(abs, rabs+string(os.PathSeparator)) {
			return abs, true
		}
	}
	return "", false
}

func (h *Handler) rpcHandler(c *gin.Context) {
	sessionID := c.GetHeader(headerTransmissionSessionID)

	userID, ok := h.resolveSessionUser(c, sessionID)
	if !ok {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRPCBodyBytes)
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

	// Convert params keys: download_dir → download-dir (internal format).
	// JSON-RPC 2.0 calls may legitimately omit "params" (e.g. session-get,
	// session-stats, port-test) — treat a nil params as an empty arg map so
	// those dispatch normally instead of failing with -32602.
	params := req.Params
	if params == nil {
		params = map[string]interface{}{}
	}
	converted := convertMapKeys(params, snakeToKebab)
	paramsMap, ok := converted.(map[string]interface{})
	if !ok {
		log.Printf("transmissionrpc: convertMapKeys did not return map[string]interface{} (got %T)", converted)
		c.JSON(http.StatusBadRequest, jsonRPCResp{
			JSONRPC: "2.0",
			Error: &jsonRPCErr{
				Code:    -32602, // Invalid params
				Message: "invalid parameters format",
			},
			ID: req.ID,
		})
		return
	}
	params = paramsMap

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
