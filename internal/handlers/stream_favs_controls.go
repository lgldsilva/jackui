package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// StreamPrefetch handles POST /api/stream/prefetch/:hash/:file — best-effort
// background fetch of a file that is NOT being streamed right now. Used by the
// player to warm up the next episode (or next playlist item, when same torrent)
// at ~50% of the current item so the transition is seamless.
//
// Returns 202 immediately; the actual piece download happens asynchronously.
func StreamPrefetch(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		fileIdx, ok := bindFileIndex(c, "file")
		if !ok {
			return
		}
		if err := s.Prefetch(h, fileIdx); err != nil {
			httpshared.RespondError(c, http.StatusNotFound, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"status": "prefetching"})
	}
}

// StreamDrop handles DELETE /api/stream/:hash — manually stop a torrent.
// Also tears down that torrent's HLS sessions (#17): closing the player must
// not leave an orphan transcode ffmpeg burning CPU until the idle reaper.
// When the dropped hash backs a completed download row, we also mark that row
// seed-stopped so the next boot's autoSeedCompleted does not resurrect it.
//
// Honest status: a refusal (viewer lease / background download) now surfaces as
// 409 — previously the handler replied 200 "dropped" even when Streamer.Drop
// silently refused, and the UI could not tell the user why nothing happened
// (incident 2026-09-28: 6× DELETE → 6× 200, torrent never stopped because the
// *arr torrent-get poll kept the 60s read guard armed).
func StreamDrop(s *streamer.Streamer, hlsMgr *transcode.HLSSessionManager, store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		// DropSeed (not Drop): removing the torrent is an explicit user action,
		// so the persisted auto-seed is cleared too — otherwise it would seed
		// again on the next boot and reappear as "active".
		if err := dropStreamHash(s, hlsMgr, h); streamer.IsDropRefusal(err) {
			httpshared.RespondErrorMessage(c, http.StatusConflict, err.Error())
			return
		}
		if store != nil {
			userID, _, _ := auth.UserIDFromCtx(c)
			_ = store.StopSeedByInfoHash(userID, h.HexString())
		}
		c.JSON(http.StatusOK, gin.H{"message": "dropped"})
	}
}

// streamDropBatchMax caps hashes per POST /stream/drop/batch (Perf #7).
const streamDropBatchMax = 300

// StreamDropBatch handles POST /api/stream/drop/batch {hashes:[...]} →
// {dropped,total,failed} — drops MANY torrents (and their HLS sessions) in ONE
// call so mass-delete on Downloads does not fire N DELETE /stream/:hash (Perf #7).
// Hashes are deduped; invalid entries land in failed without aborting the batch.
// Completed download rows backing the dropped hashes are marked seed-stopped.
func StreamDropBatch(s *streamer.Streamer, hlsMgr *transcode.HLSSessionManager, store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Hashes []string `json:"hashes"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || len(req.Hashes) == 0 {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "hashes is required")
			return
		}
		if len(req.Hashes) > streamDropBatchMax {
			httpshared.RespondErrorMessage(c, http.StatusRequestEntityTooLarge, "too many hashes")
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		dropped, failed := dropStreamHashes(s, hlsMgr, store, userID, req.Hashes)
		c.JSON(http.StatusOK, gin.H{"dropped": dropped, "total": len(req.Hashes), "failed": failed})
	}
}

// dropStreamHashes runs the batch drop loop for StreamDropBatch: dedupes the
// raw hashes, drops each one once, and returns (dropped, failed). failed keeps
// the RAW input strings so the client can match them back — both invalid hex
// and refusals (viewer lease / background download) land there; a refused
// hash is NOT marked seed-stopped because the torrent is still alive.
func dropStreamHashes(s *streamer.Streamer, hlsMgr *transcode.HLSSessionManager, store *downloads.Store, userID int, raws []string) (int, []string) {
	seen := make(map[string]struct{}, len(raws))
	dropped := 0
	failed := make([]string, 0)
	for _, raw := range raws {
		h, err := parseHash(raw)
		if err != nil {
			failed = append(failed, raw)
			continue
		}
		key := h.HexString()
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if !dropStreamHashRow(s, hlsMgr, store, userID, h) {
			failed = append(failed, raw)
			continue
		}
		dropped++
	}
	return dropped, failed
}

// dropStreamHashRow drops one hash and, when the torrent is actually gone,
// marks the backing completed download row seed-stopped. Returns false on a
// refusal (the torrent is still held by a viewer/background download).
func dropStreamHashRow(s *streamer.Streamer, hlsMgr *transcode.HLSSessionManager, store *downloads.Store, userID int, h metainfo.Hash) bool {
	if err := dropStreamHash(s, hlsMgr, h); streamer.IsDropRefusal(err) {
		return false
	}
	if store != nil {
		_ = store.StopSeedByInfoHash(userID, h.HexString())
	}
	return true
}

// dropStreamHash tears down swarm seed + HLS for one info-hash (shared by
// StreamDrop and StreamDropBatch). Returns the DropSeed outcome: nil on drop,
// ErrTorrentNotActive when already gone (idempotent), or a refusal error. The
// HLS teardown runs only when the torrent is actually gone — a refusal means a
// player still holds the torrent and killing its transcode would break it.
func dropStreamHash(s *streamer.Streamer, hlsMgr *transcode.HLSSessionManager, h metainfo.Hash) error {
	err := s.DropSeed(h)
	if hlsMgr != nil && !streamer.IsDropRefusal(err) {
		hlsMgr.CloseForHash(h.HexString())
	}
	return err
}

// StreamViewerOpen handles POST /api/stream/:hash/viewer — registers an open
// player session (a viewer "lease"). While at least one viewer is open the
// torrent keeps streaming; when the last one closes it is dropped after a short
// grace period instead of seeding indefinitely until the idle reaper.
func StreamViewerOpen(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		s.AcquireViewer(h)
		c.JSON(http.StatusOK, gin.H{"message": "viewing"})
	}
}

// StreamViewerClose handles DELETE /api/stream/:hash/viewer — releases a viewer
// lease. If it was the last viewer of a stream-only torrent, the drop is
// scheduled and the HLS session is torn down so ffmpeg doesn't linger.
func StreamViewerClose(s *streamer.Streamer, hlsMgr *transcode.HLSSessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		// Close the HLS transcode whenever the LAST viewer leaves — even if the
		// torrent stays alive to seed (seed-tracker) or finish a background
		// download. The transcode only feeds the player; leaving it running burned
		// CPU + a GPU-decode slot for nobody until the idle reaper (5min).
		if _, lastViewer := s.ReleaseViewer(h); lastViewer && hlsMgr != nil {
			hlsMgr.CloseForHash(h.HexString())
		}
		c.JSON(http.StatusOK, gin.H{"message": "released"})
	}
}

// StreamFavorite handles POST /api/stream/favorite — body: {name, infoHash, magnet, reason}
func StreamFavorite(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Name     string `json:"name"`
			InfoHash string `json:"infoHash"`
			Magnet   string `json:"magnet"`
			Reason   string `json:"reason"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrNameRequired)
			return
		}
		if req.Reason == "" {
			req.Reason = "manual"
		}
		// Normalize the hash before storing: the hidden curtain (and the
		// favorite↔library linkage generally) joins on lowercase 40-hex, so a
		// hash saved with unusual casing — or garbage — could never be matched
		// and a hidden favourite would leak through by hash. Unparsable input
		// becomes '' (name-only favorite) instead of dead weight.
		if h, herr := parseHash(strings.ToLower(strings.TrimSpace(req.InfoHash))); herr == nil {
			req.InfoHash = h.HexString()
		} else {
			req.InfoHash = ""
		}
		favs := s.Favorites()
		if favs == nil {
			httpshared.RespondErrorMessage(c, http.StatusServiceUnavailable, "favorites store not initialized")
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if err := favs.Add(req.Name, req.InfoHash, req.Magnet, req.Reason, userID); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "favorited"})
	}
}

// StreamUnfavorite handles DELETE /api/stream/favorite/:name
func StreamUnfavorite(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("name")
		favs := s.Favorites()
		if favs == nil {
			httpshared.RespondErrorMessage(c, http.StatusServiceUnavailable, "favorites store not initialized")
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && queryBool(c, "all")
		if err := favs.Remove(name, userID, includeAll); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "unfavorited"})
	}
}

// StreamFavorites handles GET /api/stream/favorites — list user's favorites.
// Admin with ?all=1 sees everyone's.
func StreamFavorites(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		favs := s.Favorites()
		if favs == nil {
			c.JSON(http.StatusOK, []streamer.Favorite{})
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && queryBool(c, "all")
		// The global reveal curtain (X-JackUI-Reveal-Hidden, the easter egg) or the
		// legacy ?includeHidden=1 reveal favourites inside hidden folders.
		list, err := favs.List(userID, includeAll, middleware.IsRevealHidden(c) || c.Query("includeHidden") == "1")
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		if list == nil {
			list = []streamer.Favorite{}
		}
		enrichFavoritesSortMeta(s, list)
		c.JSON(http.StatusOK, list)
	}
}

// enrichFavoritesSortMeta fills each favourite's TotalSize/Seeders from the
// metadata cache (a separate DB) in one batch query, so the UI can sort by size
// or seeds. Unknown values stay zero/nil and sort last on the client.
func enrichFavoritesSortMeta(s *streamer.Streamer, list []streamer.Favorite) {
	cache := s.MetadataCache()
	if cache == nil || len(list) == 0 {
		return
	}
	hashes := make([]string, 0, len(list))
	for _, f := range list {
		if f.InfoHash != "" {
			hashes = append(hashes, f.InfoHash)
		}
	}
	meta := cache.GetSortMeta(hashes)
	for i := range list {
		m, ok := meta[list[i].InfoHash]
		if !ok {
			continue
		}
		list[i].TotalSize = m.TotalSize
		if m.Seeders >= 0 {
			seeders := m.Seeders
			list[i].Seeders = &seeders
		}
	}
}

// ─── Transmission-style download controls ──────────────────────────────────

// StreamPause handles POST /api/stream/:hash/pause — soft-pause peer connections.
func StreamPause(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		if err := s.Pause(h); err != nil {
			httpshared.RespondError(c, http.StatusNotFound, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "paused"})
	}
}

// StreamResume handles POST /api/stream/:hash/resume — re-enable peer connections.
func StreamResume(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		if err := s.Resume(h); err != nil {
			httpshared.RespondError(c, http.StatusNotFound, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "resumed"})
	}
}

// StreamSetPriority handles POST /api/stream/:hash/priority — body {priority}.
func StreamSetPriority(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		var req struct {
			Priority string `json:"priority"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		if err := s.SetPriority(h, req.Priority); err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, streamer.ErrTorrentNotActive) {
				code = http.StatusNotFound
			}
			httpshared.RespondError(c, code, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"priority": strings.ToLower(req.Priority)})
	}
}

// StreamSetFilePriority handles POST /api/stream/:hash/files/:idx/priority — body {priority}.
func StreamSetFilePriority(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		idx, err := strconv.Atoi(c.Param("idx"))
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, errInvalidFileIndex)
			return
		}
		var req struct {
			Priority string `json:"priority"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		if err := s.SetFilePriority(h, idx, req.Priority); err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, streamer.ErrTorrentNotActive) {
				code = http.StatusNotFound
			}
			httpshared.RespondError(c, code, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"priority": strings.ToLower(req.Priority)})
	}
}

// StreamActive handles GET /api/stream/active — snapshot of every active
// torrent. The swarm itself is shared by design (Downloads polls this to show
// and stop anything live), but the requester's hidden-favourite curtain still
// applies to the LISTING: hiding a title must keep it off the active view even
// while it streams. The easter egg (X-JackUI-Reveal-Hidden) reveals everything.
func StreamActive(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		list := s.ActiveList()
		if list == nil {
			list = []*streamer.TorrentInfo{}
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		curtain := hiddenCurtain(c, s, userID, false)
		c.JSON(http.StatusOK, dropHiddenActive(list, curtain))
	}
}

// dropHiddenActive removes active-swarm entries matching the requester's
// identity curtain (info_hash OR normalized name). Pure → unit-testable
// (NewForTesting exposes no way to seed ActiveList from the handlers package).
func dropHiddenActive(list []*streamer.TorrentInfo, curtain streamer.HiddenCurtain) []*streamer.TorrentInfo {
	if curtain.Empty() {
		return list
	}
	out := make([]*streamer.TorrentInfo, 0, len(list))
	for _, t := range list {
		if t == nil || curtainHidden(t.InfoHash, t.Name, curtain) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// StreamPauseAll handles POST /api/stream/active/pause — bulk pause.
func StreamPauseAll(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		n := s.PauseAll()
		c.JSON(http.StatusOK, gin.H{"paused": n})
	}
}

// StreamResumeAll handles POST /api/stream/active/resume — bulk resume.
func StreamResumeAll(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		n := s.ResumeAll()
		c.JSON(http.StatusOK, gin.H{"resumed": n})
	}
}

// StreamGetLimits handles GET /api/stream/limits — current global bandwidth caps.
func StreamGetLimits(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		down, up := s.RateLimits()
		c.JSON(http.StatusOK, gin.H{"down": down, "up": up})
	}
}

// StreamSetLimits handles POST /api/stream/limits — body {down, up} in bytes/sec.
// 0 = unlimited; negative values rejected.
func StreamSetLimits(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Down int64 `json:"down"`
			Up   int64 `json:"up"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		if req.Down < 0 || req.Up < 0 {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "limits must be >= 0 (0 = unlimited)")
			return
		}
		s.SetRateLimits(req.Down, req.Up)
		c.JSON(http.StatusOK, gin.H{"down": req.Down, "up": req.Up})
	}
}
