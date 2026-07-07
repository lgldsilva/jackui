package handlers

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// parseMagnetTrackers extracts &tr= tracker URLs from a magnet URI.
// The anacrolix runtime uses these but they are NOT in the torrent's stored
// metainfo, so UpvertedAnnounceList() misses them entirely.
func parseMagnetTrackers(magnet string) []string {
	if magnet == "" {
		return nil
	}
	// Magnet URIs aren't valid URLs, but we can parse the query string.
	rest := magnet
	if idx := strings.Index(magnet, "?"); idx >= 0 {
		rest = magnet[idx+1:]
	}
	vals, err := url.ParseQuery(rest)
	if err != nil {
		return nil
	}
	return vals["tr"]
}

// userCache is a simple in-memory cache for username lookups during a single
// request. Avoids N+1 queries to the auth store for each download row.
type userCache map[int]string

func (uc userCache) get(store *auth.Store, userID int) string {
	if s, ok := uc[userID]; ok {
		return s
	}
	if store == nil {
		return ""
	}
	u, err := store.GetUserByID(userID)
	if err != nil {
		return ""
	}
	uc[userID] = u.Username
	return u.Username
}

// liveStats holds a torrent's live (non-persisted) metrics. They're per-torrent,
// so every selected file of one torrent shares them.
type liveStats struct {
	down, up int64
	uploaded int64 // cumulative bytes served this session (anacrolix BytesWrittenData)
	seeders  int
}

// enrichETA populates the live metrics + ETA for a download by looking up the
// active torrent info from the streamer. No-op when streamer is nil or the
// torrent isn't active — the row's existing values are preserved.
func enrichETA(d *downloads.Download, s *streamer.Streamer) {
	if s == nil || d.InfoHash == "" || d.FileSize <= 0 {
		return
	}
	if st, ok := liveStatsOf(s, d.InfoHash); ok {
		applyLive(d, st)
	}
}

// applyLive sets DownRate/UpRate/Seeders + ETA on a row from its torrent's
// shared stats. The UI sorts by down/up rate and seeders client-side (they're
// live, not stored, so they can't be ORDER BY'd in SQL).
func applyLive(d *downloads.Download, st liveStats) {
	d.DownRate, d.UpRate, d.Seeders = st.down, st.up, st.seeders
	d.BytesUploaded = st.uploaded
	if st.down <= 0 {
		return
	}
	if remaining := d.FileSize - d.BytesDownloaded; remaining > 0 {
		d.ETA = int(remaining / st.down)
	}
}

// enrichETAList fills the live metrics + ETA for the slice, looking each torrent
// up in the streamer ONCE per unique info_hash. Many rows are selected files of
// the SAME torrent (a multi-file pack is hundreds of rows), and s.Get→buildInfo
// is O(files); doing it per row was O(rows×files) and locked the torrent client
// thousands of times — which made GET /api/downloads take MINUTES on a big pack.
// Deduping by hash makes it O(unique active torrents).
//
// NOTE: This relies on the implicit invariant that all files of the same torrent
// (sharing the same InfoHash) report the same aggregated torrent downRate/upRate
// and seeders. Caching stats by hash (byHash) ensures that even if list sorting
// is unstable, the rate applied to any sibling row is identical and stable.
func enrichETAList(list []downloads.Download, s *streamer.Streamer) {
	if s == nil {
		return
	}
	byHash := make(map[string]liveStats)
	for i := range list {
		d := &list[i]
		if d.InfoHash == "" || d.FileSize <= 0 {
			continue
		}
		st, seen := byHash[d.InfoHash]
		if !seen {
			st, _ = liveStatsOf(s, d.InfoHash) // zero value when inactive — rows default to 0 anyway
			byHash[d.InfoHash] = st
		}
		applyLive(d, st)
	}
}

// liveStatsOf returns a torrent's current down/up rate + seeders. ok is false
// when the torrent isn't active (or the hash is malformed) — callers preserve
// the row's existing values in that case. One streamer lookup; callers cache it
// by hash.
func liveStatsOf(s *streamer.Streamer, infoHash string) (liveStats, bool) {
	var h metainfo.Hash
	if err := h.FromHexString(infoHash); err != nil {
		return liveStats{}, false
	}
	// LiveStats (not Get) — Get→buildInfo is O(files) and a multi-file pack
	// (Morgpie: 778 files) made enriching the list take many seconds under load.
	// LiveStats is O(1) per torrent: just the cached rate sample + Stats().
	down, up, uploaded, seeders, ok := s.LiveStats(h)
	if !ok {
		return liveStats{}, false
	}
	return liveStats{down: down, up: up, uploaded: uploaded, seeders: seeders}, true
}

// markPromoted sets Promoted=true for completed downloads whose FilePath is
// outside the download dir (i.e. the file was moved to a library/GDrive).
func markPromoted(list []downloads.Download, downloadDir string) {
	if downloadDir == "" {
		return
	}
	for i := range list {
		d := &list[i]
		if d.Status == downloads.StatusCompleted && d.FilePath != "" &&
			!strings.HasPrefix(d.FilePath, downloadDir) {
			d.Promoted = true
		}
	}
}

// DownloadsList handles GET /api/downloads — current user's queue.
func DownloadsList(store *downloads.Store, streamer *streamer.Streamer, browser *local.Browser, authStore *auth.Store, downloadDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		list, err := store.List(userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		list = dropHiddenDownloads(list, buildHiddenDownloadFilter(c, streamer, browser, authStore, userID, false))
		enrichETAList(list, streamer)
		markPromoted(list, downloadDir)
		downloads.AssignQueuePositions(list)
		c.JSON(http.StatusOK, list)
	}
}

// DownloadsListFiltered handles GET /api/downloads/filtered — returns
// downloads filtered by query params: status, tracker, category, search,
// sort, order. Also returns available trackers/categories for filter UI.
func DownloadsListFiltered(store *downloads.Store, streamer *streamer.Streamer, browser *local.Browser, authStore *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		status := c.Query("status")
		tracker := c.Query("tracker")
		category := c.Query("category")
		search := c.Query("search")
		sortCol := c.DefaultQuery("sort", "created_at")
		sortDir := c.DefaultQuery("order", "desc")

		list, err := store.ListFiltered(downloads.ListFilter{
			UserID:   userID,
			Status:   status,
			Tracker:  tracker,
			Category: category,
			Search:   search,
			SortCol:  sortCol,
			SortDir:  sortDir,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		list = dropHiddenDownloads(list, buildHiddenDownloadFilter(c, streamer, browser, authStore, userID, false))
		enrichETAList(list, streamer)
		downloads.AssignQueuePositions(list)
		c.JSON(http.StatusOK, list)
	}
}

// DownloadsListAll handles GET /api/downloads/all — admin-only: returns
// downloads from ALL users, enriched with usernames. Supports the same
// filtering params as DownloadsListFiltered, plus userId filter.
func DownloadsListAll(dlStore *downloads.Store, authStore *auth.Store, streamer *streamer.Streamer, browser *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		status := c.Query("status")
		tracker := c.Query("tracker")
		category := c.Query("category")
		search := c.Query("search")
		sortCol := c.DefaultQuery("sort", "created_at")
		sortDir := c.DefaultQuery("order", "desc")
		userIDFilter := c.Query("userId")

		list, err := dlStore.ListFilteredAll(downloads.ListFilter{
			Status:       status,
			Tracker:      tracker,
			Category:     category,
			Search:       search,
			UserIDFilter: userIDFilter,
			SortCol:      sortCol,
			SortDir:      sortDir,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		list = dropHiddenDownloads(list, buildHiddenDownloadFilter(c, streamer, browser, authStore, 0, true))
		uc := userCache{}
		for i := range list {
			list[i].Username = uc.get(authStore, list[i].UserID)
		}
		enrichETAList(list, streamer)
		downloads.AssignQueuePositions(list)

		c.JSON(http.StatusOK, list)
	}
}

// DownloadsUsers handles GET /api/downloads/users — admin-only: returns the
// list of users that have downloads, for the filter dropdown.
func DownloadsUsers(dlStore *downloads.Store, authStore *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userIDs, err := dlStore.DistinctUsers()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		type userEntry struct {
			ID       int    `json:"id"`
			Username string `json:"username"`
		}
		uc := userCache{}
		out := make([]userEntry, 0, len(userIDs))
		for _, uid := range userIDs {
			out = append(out, userEntry{ID: uid, Username: uc.get(authStore, uid)})
		}
		c.JSON(http.StatusOK, out)
	}
}

// DownloadsCreate handles POST /api/downloads — enqueues a new full-file
// download. Body: { infoHash, fileIndex, magnet, name, filePath, fileSize, tracker?, category? }
func DownloadsCreate(store *downloads.Store, dests *DestinationService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			InfoHash   string `json:"infoHash"`
			FileIndex  int    `json:"fileIndex"`
			Magnet     string `json:"magnet"`
			Name       string `json:"name"`
			FilePath   string `json:"filePath"`
			FileSize   int64  `json:"fileSize"`
			Tracker    string `json:"tracker,omitempty"`
			Category   string `json:"category,omitempty"`
			DestBase   string `json:"destBase,omitempty"`   // chosen destination (#16); empty = default
			DestSubdir string `json:"destSubdir,omitempty"` // optional subfolder under DestBase
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.InfoHash == "" || req.Magnet == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "infoHash and magnet are required"})
			return
		}
		// Valid negatives are the documented sentinels only (-1 auto-pick,
		// -2 whole torrent); anything below is a malformed request.
		if req.FileIndex < downloads.FileIndexWholeTorrent {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid fileIndex"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		// Validate the chosen destination against the user's allowed destinations
		// (rejects an arbitrary path); empty base → default download dir.
		base, subdir, err := dests.Resolve(userID, req.DestBase, req.DestSubdir)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		d, err := store.Create(downloads.Download{
			UserID:     userID,
			InfoHash:   req.InfoHash,
			FileIndex:  req.FileIndex,
			FilePath:   req.FilePath,
			FileSize:   req.FileSize,
			Name:       req.Name,
			Magnet:     req.Magnet,
			Tracker:    req.Tracker,
			Category:   req.Category,
			DestBase:   base,
			DestSubdir: subdir,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, d)
	}
}

// DownloadsBatchCreate handles POST /api/downloads/batch — enqueues every selected
// file of ONE torrent in a single transaction (replaces N single-file POSTs). The
// torrent identity (infoHash/magnet/name/tracker/category) and the destination are
// shared; only the per-file fields vary. Body:
//
//	{ infoHash, magnet, name, tracker?, category?, destBase?, destSubdir?,
//	  files: [{ fileIndex, filePath, fileSize }] }
//
// Returns { created: [...], requeued: n } — `requeued` counts files that already
// existed (the row came back unchanged or re-queued rather than freshly inserted).
func DownloadsBatchCreate(store *downloads.Store, dests *DestinationService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			InfoHash   string `json:"infoHash"`
			Magnet     string `json:"magnet"`
			Name       string `json:"name"`
			Tracker    string `json:"tracker,omitempty"`
			Category   string `json:"category,omitempty"`
			DestBase   string `json:"destBase,omitempty"`
			DestSubdir string `json:"destSubdir,omitempty"`
			Files      []struct {
				FileIndex int    `json:"fileIndex"`
				FilePath  string `json:"filePath"`
				FileSize  int64  `json:"fileSize"`
			} `json:"files"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.InfoHash == "" || req.Magnet == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "infoHash and magnet are required"})
			return
		}
		if len(req.Files) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "files must not be empty"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		// Validate the chosen destination ONCE — it's shared by every file.
		base, subdir, err := dests.Resolve(userID, req.DestBase, req.DestSubdir)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		rows := make([]downloads.Download, 0, len(req.Files))
		for _, f := range req.Files {
			rows = append(rows, downloads.Download{
				UserID:     userID,
				InfoHash:   req.InfoHash,
				FileIndex:  f.FileIndex,
				FilePath:   f.FilePath,
				FileSize:   f.FileSize,
				Name:       req.Name,
				Magnet:     req.Magnet,
				Tracker:    req.Tracker,
				Category:   req.Category,
				DestBase:   base,
				DestSubdir: subdir,
			})
		}
		res, err := store.BatchCreate(rows)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"created": res.Rows, "requeued": res.Requeued})
	}
}

// DownloadRemover is the slice of the downloads worker the delete handlers
// need: synchronously tear down in-memory tracking + drop the torrent for a
// deleted row, so the deletion is authoritative the instant the handler
// returns (no 2s tick lag, no resurrection by an in-flight init). An interface
// keeps the handlers testable without constructing a real worker. nil is a
// valid value (the worker may not be running) — callers guard for it.
type DownloadRemover interface {
	Remove(id int, infoHash string)
}

// DownloadsDelete handles DELETE /api/downloads/:id — cancel + remove row.
//
// The delete is AUTHORITATIVE and admin-aware: an admin in the "all users" view
// can remove any user's row (DeleteScoped with isAdmin). It's also idempotent —
// deleting an already-gone row returns 204, not a 500 (the old behavior the
// frontend swallowed, leaving the row to reappear on the next poll).
func DownloadsDelete(store *downloads.Store, worker DownloadRemover) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		row, err := store.DeleteScoped(userID, id, isAdmin)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		notifyRemoved(worker, row)
		c.Status(http.StatusNoContent)
	}
}

// notifyRemoved tells the worker to tear down a just-deleted row's in-memory
// state and drop its torrent. No-op when the worker isn't wired or the row was
// already gone (idempotent delete).
func notifyRemoved(worker DownloadRemover, row *downloads.Download) {
	if worker == nil || row == nil {
		return
	}
	worker.Remove(row.ID, row.InfoHash)
}

// DownloadsPause handles PATCH /api/downloads/:id/pause — flips status to paused.
// The worker's next tick will untrack the row and unregister the streamer
// protection, but the on-disk bytes already fetched stay there.
func DownloadsPause(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		// Ownership check first
		if _, err := store.Get(userID, id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": ErrNotFound})
			return
		}
		if err := store.SetStatus(userID, id, downloads.StatusPaused); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// DownloadsResume handles PATCH /api/downloads/:id/resume — re-queues the
// download. The scheduler promotes it to downloading once a slot is free
// (honoring the active limit), instead of jumping straight past the queue.
func DownloadsResume(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if _, err := store.Get(userID, id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": ErrNotFound})
			return
		}
		if err := store.Requeue(userID, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// DownloadsSetPriority handles PATCH /api/downloads/:id/priority — sets the
// queue priority (high/normal/low). Takes effect on the next scheduler tick.
func DownloadsSetPriority(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		var req struct {
			Priority string `json:"priority"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if _, err := store.Get(userID, id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": ErrNotFound})
			return
		}
		if err := store.SetPriority(userID, id, req.Priority); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// DownloadsSources handles GET /api/downloads/:id/sources — the catalog of
// known sources (original + alternatives) for a download. Empty until source
// rotation (Phase 2) has run. Ownership-checked.
func DownloadsSources(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if _, err := store.Get(userID, id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": ErrNotFound})
			return
		}
		sources, err := store.ListSources(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if sources == nil {
			sources = []downloads.Source{}
		}
		c.JSON(http.StatusOK, sources)
	}
}

// DownloadsTrackers handles GET /api/downloads/trackers — distinct trackers.
func DownloadsTrackers(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		trackers, err := store.DistinctTrackers(userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, trackers)
	}
}

// DownloadsCategories handles GET /api/downloads/categories — distinct categories.
func DownloadsCategories(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		cats, err := store.DistinctCategories(userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, cats)
	}
}

// DownloadsPauseAll handles PATCH /api/downloads/pause-all — pause all
// non-terminal downloads for the current user.
func DownloadsPauseAll(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		n, err := store.SetStatusForUser(userID, downloads.StatusPaused)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"affected": n})
	}
}

// DownloadsResumeAll handles PATCH /api/downloads/resume-all — re-queues all
// paused downloads for the current user (scheduler honors the active limit).
func DownloadsResumeAll(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		n, err := store.RequeueForUser(userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"affected": n})
	}
}

// DownloadsBatchPause handles PATCH /api/downloads/batch/pause — pause
// specific downloads by IDs. Body: { ids: [1, 2, 3] }
func DownloadsBatchPause(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			IDs []int `json:"ids"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		n, err := store.SetStatusByIDs(userID, req.IDs, downloads.StatusPaused)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"affected": n})
	}
}

// DownloadsBatchResume handles PATCH /api/downloads/batch/resume — resume
// specific downloads by IDs. Body: { ids: [1, 2, 3] }
func DownloadsBatchResume(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			IDs []int `json:"ids"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		n, err := store.RequeueByIDs(userID, req.IDs)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"affected": n})
	}
}

// DownloadsBatchDelete handles POST /api/downloads/batch/delete — delete
// specific downloads by IDs. Body: { ids: [1, 2, 3] }
//
// Admin-aware (DeleteScoped honors isAdmin so the "all users" view can remove
// any row) and authoritative: each successful delete tears down the worker's
// in-memory state + drops the torrent. `failed` surfaces IDs the store errored
// on (vs. already-gone rows, which count as deleted) so the frontend can warn
// instead of silently leaving them to reappear on the next poll.
func DownloadsBatchDelete(store *downloads.Store, worker DownloadRemover) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			IDs []int `json:"ids"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		deleted := 0
		failed := make([]int, 0)
		for _, id := range req.IDs {
			row, err := store.DeleteScoped(userID, id, isAdmin)
			if err != nil {
				failed = append(failed, id)
				continue
			}
			deleted++
			notifyRemoved(worker, row)
		}
		c.JSON(http.StatusOK, gin.H{"deleted": deleted, "total": len(req.IDs), "failed": failed})
	}
}

// DownloadsRecheck handles POST /api/downloads/:id/recheck — força um
// "Force Recheck" estilo qBittorrent no arquivo do download: re-hasha TODOS
// os pieces do arquivo (não só os incompletos), zera bytes_downloaded e
// volta o status pra `downloading` pra que o worker reconcilie com a verdade
// do disco depois. Uso típico: usuário desconfia que os bytes corromperam
// (BitErrors, ungraceful shutdown sem grace period), ou o file_size do row
// não bate com o real.
func DownloadsRecheck(store *downloads.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		d, err := store.Get(userID, id)
		if err != nil || d == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": errDownloadNotFound})
			return
		}
		var h metainfo.Hash
		if err := h.FromHexString(d.InfoHash); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "infoHash inválido"})
			return
		}
		// EnsureActive antes do recheck — se o torrent foi dropado (ex.: post-
		// completed sem seed), precisa re-attach pra ter acesso aos files.
		if d.Magnet != "" {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
			_, err := s.EnsureActive(ctx, d.Magnet)
			cancel()
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
				return
			}
		}
		// Whole-torrent rows re-hash every file; per-file rows only theirs.
		recheck := func() error { return s.RecheckFile(h, d.FileIndex) }
		if d.IsWholeTorrent() {
			recheck = func() error { return s.RecheckAllFiles(h) }
		}
		if err := recheck(); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		// Reset row pro worker reconciliar com o real após o hash check. Vai pra
		// fila (não direto pra downloading) pro scheduler respeitar o limite.
		if err := store.UpdateProgress(userID, id, 0); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := store.SetStatus(userID, id, downloads.StatusQueued); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		updated, err := store.Get(userID, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, updated)
	}
}

// DownloadsDetails handles GET /api/downloads/:id/details — devolve o row do
// download + info do torrent (todos os arquivos, peers/seeders ao vivo,
// tamanhos reais no disco). Usado pelo modal de inspeção pra mostrar o que
// o torrent tem além do arquivo baixado, distinguir aparente (sparse) de
// real, e habilitar ações por arquivo.
type downloadFileStat struct {
	Apparent int64 `json:"apparent"`
	OnDisk   int64 `json:"onDisk"`
	Exists   bool  `json:"exists"`
}

func getDownloadFileStat(filePath string) downloadFileStat {
	var stat downloadFileStat
	if filePath == "" {
		return stat
	}
	st, err := os.Stat(filePath)
	if err != nil {
		return stat
	}
	stat.Apparent = st.Size()
	stat.OnDisk = streamer.PhysicalBytes(st)
	stat.Exists = true
	return stat
}

func getDownloadTorrentInfo(s *streamer.Streamer, infoHash, magnet string) *streamer.TorrentInfo {
	var info *streamer.TorrentInfo
	var h metainfo.Hash
	if err := h.FromHexString(infoHash); err == nil {
		if got, gerr := s.Get(h); gerr == nil {
			info = got
		}
	}
	magnetTrackers := parseMagnetTrackers(magnet)
	if len(magnetTrackers) == 0 {
		return info
	}
	if info != nil {
		existing := make(map[string]bool, len(info.Trackers))
		for _, t := range info.Trackers {
			existing[t] = true
		}
		for _, t := range magnetTrackers {
			if !existing[t] {
				info.Trackers = append(info.Trackers, t)
			}
		}
		return info
	}
	return &streamer.TorrentInfo{Trackers: magnetTrackers}
}

// DownloadsPeers returns the live connected-peer list for a download's torrent.
// When the torrent isn't active (dropped/never opened) it returns an empty list
// with active=false instead of an error, so the polling UI can show "inactive".
func DownloadsPeers(store *downloads.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		d, err := store.Get(userID, id)
		if err != nil || d == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": errDownloadNotFound})
			return
		}
		peers := []streamer.PeerInfo{}
		active := false
		var h metainfo.Hash
		if herr := h.FromHexString(d.InfoHash); herr == nil && s != nil {
			if got, perr := s.Peers(h); perr == nil {
				peers = got
				active = true
			}
		}
		c.JSON(http.StatusOK, gin.H{"peers": peers, "active": active})
	}
}

func DownloadsDetails(store *downloads.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		d, err := store.Get(userID, id)
		if err != nil || d == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": errDownloadNotFound})
			return
		}

		stat := getDownloadFileStat(d.FilePath)
		info := getDownloadTorrentInfo(s, d.InfoHash, d.Magnet)

		c.JSON(http.StatusOK, gin.H{
			"download": d,
			"file":     stat,
			"torrent":  info,
		})
	}
}
