package transmissionrpc

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
)

// rpcSessionProbe answers the *arr's session-id GET probe: resolveSessionUser
// emits 409 + X-Transmission-Session-Id when there's no established session
// (the handshake the client wants). A GET carrying a valid session has no RPC
// body to run, so we just acknowledge it.
func (h *Handler) rpcSessionProbe(c *gin.Context) {
	sessionID := c.GetHeader(headerTransmissionSessionID)
	if _, ok := h.resolveSessionUser(c, sessionID); ok {
		c.JSON(http.StatusOK, rpcResponse{Result: "success"})
	}
}

func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "deadbeef0102030405060708090a0b0c"
	}
	return hex.EncodeToString(b)
}

func (h *Handler) emit409(c *gin.Context, sid string) {
	c.Header(headerTransmissionSessionID, sid)
	c.Header("X-Transmission-Rpc-Version", "6.0.1")
	c.JSON(http.StatusConflict, rpcResponse{Result: "success"})
}

// resolveSessionUser authenticates the caller via session-id or Basic Auth.
// Returns (identity, false) after emitting 409 (handshake needed) — or after
// answering the request itself with 401 (guest role refused) / 429 (locked
// out by repeated password failures).
func (h *Handler) resolveSessionUser(c *gin.Context, sessionID string) (rpcIdentity, bool) {
	if h.authStore == nil {
		// Auth disabled: local-trusted deployment; the system identity has
		// full cross-user visibility.
		return rpcIdentity{userID: 0, admin: true}, true
	}
	h.mu.RLock()
	uid, known := h.sessions[sessionID]
	admin := h.roles[uid]
	h.mu.RUnlock()
	if known {
		return rpcIdentity{userID: uid, admin: admin}, true
	}
	sid := newSessionID()
	if user, pass, pwok := c.Request.BasicAuth(); pwok {
		if h.rejectIfLocked(c, user) {
			return rpcIdentity{}, false
		}
		u, err := h.authStore.VerifyPassword(user, pass)
		switch {
		case err != nil || u == nil:
			// Bad credentials still answer 409 (the *arr handshake the client
			// expects), but count toward the brute-force lockout.
			if h.lockout != nil {
				h.lockout.Fail(user)
			}
		case u.Role == auth.RoleGuest:
			// Guests are read-only UI visitors; the RPC can add/remove any
			// download, so they never get past authentication.
			rejectUnauthorized(c)
			return rpcIdentity{}, false
		default:
			if h.lockout != nil {
				h.lockout.Reset(user)
			}
			h.mu.Lock()
			h.sessions[sid] = u.ID
			h.roles[u.ID] = u.Role == auth.RoleAdmin
			h.mu.Unlock()
		}
	}
	h.emit409(c, sid)
	return rpcIdentity{}, false
}

// ─── session-set ───────────────────────────────────────────────────────────

func (h *Handler) methodSessionSet(args map[string]interface{}) rpcResponse {
	h.applySessionAltSpeed(args)
	h.applySessionQueue(args)
	h.applySessionSpeedLimits(args)
	return successResp(nil)
}

// applySessionAltSpeed mutates the live session state under the handler
// mutex: the *arr session-get poll reads these fields concurrently.
func (h *Handler) applySessionAltSpeed(args map[string]interface{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
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
	h.mu.Lock()
	defer h.mu.Unlock()
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

// ─── session-get ───────────────────────────────────────────────────────────

// reportDir is the download-dir reported to the *arr for a download: the
// auto-promote target (sharedDir/<category>) when enabled for an *arr download,
// else the plain downloadDir. Keeps torrent-get's path in sync with where the
// worker actually writes the finished files (downloads.PromoteDir).
func (h *Handler) reportDir(d downloads.Download) string {
	if h.autoPromoteOn() && d.Source == downloads.SourceArr {
		return downloads.PromoteDir(h.sharedDir, d.Category)
	}
	return h.downloadDir
}

// autoPromoteOn reports whether *arr auto-promote is active (feature wired + a
// SharedDir configured + the live setting on).
func (h *Handler) autoPromoteOn() bool {
	return h.sharedDir != "" && h.autoPromote != nil && h.autoPromote()
}

func (h *Handler) methodSessionGet() rpcResponse {
	dir := h.downloadDir
	if h.autoPromoteOn() {
		dir = h.sharedDir // base of the Transmission-style completed-downloads tree
	}
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

	peerPort := 51469
	if h.streamer != nil {
		p := h.streamer.ListenPort()
		if p > 0 {
			peerPort = p
		}
	}

	// Snapshot the mutable session state under the read lock: session-set may
	// be mutating it concurrently (live *arr poll vs UI settings change).
	h.mu.RLock()
	startAdded := h.startAddedTorrents
	altSpeedEnabled := h.altSpeedEnabled
	altSpeedDown := h.altSpeedDown
	altSpeedUp := h.altSpeedUp
	dlQueueEnabled := h.downloadQueueEnabled
	dlQueueSize := h.downloadQueueSize
	seedQueueEnabled := h.seedQueueEnabled
	seedQueueSize := h.seedQueueSize
	h.mu.RUnlock()

	return successResp(map[string]interface{}{
		"version":                              "4.1.1",
		"rpc-version":                          19,
		"rpc-version-semver":                   "6.0.1",
		"rpc-version-minimum":                  14,
		"session-id":                           sessionID,
		"download-dir":                         dir,
		"download-dir-free-space":              freeSpace,
		"incomplete-dir":                       h.dataDir,
		"incomplete-dir-enabled":               true,
		"config-dir":                           "/config",
		"cache-size-mb":                        4,
		"seedRatioLimit":                       2.0,
		"seedRatioLimited":                     false,
		"peer-port":                            peerPort,
		"peer-port-random-on-start":            false,
		"peer-limit-global":                    200,
		"peer-limit-per-torrent":               50,
		"pex-enabled":                          true,
		"dht-enabled":                          true,
		"lpd-enabled":                          false,
		"utp-enabled":                          true,
		"tcp-enabled":                          true,
		"encryption":                           "preferred",
		"start-added-torrents":                 startAdded,
		"alt-speed-enabled":                    altSpeedEnabled,
		"alt-speed-down":                       altSpeedDown,
		"alt-speed-up":                         altSpeedUp,
		"alt-speed-time-begin":                 540,
		"alt-speed-time-day":                   127,
		"alt-speed-time-enabled":               false,
		"alt-speed-time-end":                   1020,
		keySpeedLimitDown:                      0,
		keySpeedLimitDownEn:                    false,
		keySpeedLimitUp:                        0,
		keySpeedLimitUpEn:                      false,
		"download-queue-enabled":               dlQueueEnabled,
		"download-queue-size":                  dlQueueSize,
		"seed-queue-enabled":                   seedQueueEnabled,
		"seed-queue-size":                      seedQueueSize,
		"queue-stalled-enabled":                true,
		"queue-stalled-minutes":                30,
		"idle-seeding-limit":                   3000,
		"idle-seeding-limit-enabled":           true,
		"anti-brute-force-enabled":             false,
		"anti-brute-force-threshold":           100,
		"blocklist-enabled":                    false,
		"blocklist-size":                       0,
		"blocklist-url":                        "http://www.example.com/blocklist",
		"default-trackers":                     "",
		"preferred_transports":                 []string{"utp", "tcp"},
		"rename-partial-files":                 true,
		"reqq":                                 2000,
		"script-torrent-added-enabled":         false,
		"script-torrent-added-filename":        "",
		"script-torrent-done-enabled":          false,
		"script-torrent-done-filename":         "",
		"script-torrent-done-seeding-enabled":  false,
		"script-torrent-done-seeding-filename": "",
		"sequential_download":                  false,
		"trash-original-torrent-files":         false,
		"port-forwarding-enabled":              false,
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
