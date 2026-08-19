package handlers

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// DownloadsList handles GET /api/downloads — current user's queue.
func DownloadsList(store *downloads.Store, streamer *streamer.Streamer, browser *local.Browser, authStore *auth.Store, downloadDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		list, err := store.List(userID)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
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
			httpshared.RespondError(c, http.StatusInternalServerError, err)
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
			httpshared.RespondError(c, http.StatusInternalServerError, err)
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
			httpshared.RespondError(c, http.StatusInternalServerError, err)
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
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		if req.InfoHash == "" || req.Magnet == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "infoHash and magnet are required")
			return
		}
		// Valid negatives are the documented sentinels only (-1 auto-pick,
		// -2 whole torrent); anything below is a malformed request.
		if req.FileIndex < downloads.FileIndexWholeTorrent {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "invalid fileIndex")
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		// Validate the chosen destination against the user's allowed destinations
		// (rejects an arbitrary path); empty base → default download dir.
		base, subdir, err := dests.Resolve(userID, req.DestBase, req.DestSubdir)
		if err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
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
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		log.Printf("downloads: create user=%d id=%d infoHash=%s fileIndex=%d name=%q", userID, d.ID, httpshared.SanitizeForLog(d.InfoHash), d.FileIndex, httpshared.SanitizeForLog(d.Name))
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
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		if req.InfoHash == "" || req.Magnet == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "infoHash and magnet are required")
			return
		}
		if len(req.Files) == 0 {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "files must not be empty")
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		// Validate the chosen destination ONCE — it's shared by every file.
		base, subdir, err := dests.Resolve(userID, req.DestBase, req.DestSubdir)
		if err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
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
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		log.Printf("downloads: batchCreate user=%d infoHash=%s files=%d created=%d requeued=%d", userID, httpshared.SanitizeForLog(req.InfoHash), len(req.Files), len(res.Rows), res.Requeued)
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
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		row, err := store.DeleteScoped(userID, id, isAdmin)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		if row != nil {
			log.Printf("downloads: delete user=%d id=%d infoHash=%s name=%q admin=%v", userID, row.ID, httpshared.SanitizeForLog(row.InfoHash), httpshared.SanitizeForLog(row.Name), isAdmin)
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
// protection, but the on-disk bytes already fetched stay there. Finished rows
// (completed/failed) are rejected with 409 — see the guard below.
func DownloadsPause(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		// Ownership check first
		row, err := store.Get(userID, id)
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusNotFound, ErrNotFound)
			return
		}
		// A finished download has nothing to pause, and flipping it to `paused`
		// stranded the row: resume refuses to leave `completed`, and the card lost
		// its completed-only actions (promote / stop-seed / open-local). Reject
		// instead of silently mangling the row — the store enforces this too.
		if row.Status == downloads.StatusCompleted || row.Status == downloads.StatusFailed {
			httpshared.RespondErrorMessage(c, http.StatusConflict, ErrNotPausable)
			return
		}
		if err := store.SetStatus(userID, id, downloads.StatusPaused); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		log.Printf("downloads: pause user=%d id=%d", userID, id)
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
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if _, err := store.Get(userID, id); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusNotFound, ErrNotFound)
			return
		}
		if err := store.Requeue(userID, id); err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		log.Printf("downloads: resume user=%d id=%d", userID, id)
		c.Status(http.StatusNoContent)
	}
}

// DownloadsSetPriority handles PATCH /api/downloads/:id/priority — sets the
// queue priority (high/normal/low). Takes effect on the next scheduler tick.
func DownloadsSetPriority(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		var req struct {
			Priority string `json:"priority"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if _, err := store.Get(userID, id); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusNotFound, ErrNotFound)
			return
		}
		if err := store.SetPriority(userID, id, req.Priority); err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
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
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if _, err := store.Get(userID, id); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusNotFound, ErrNotFound)
			return
		}
		sources, err := store.ListSources(id)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
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
			httpshared.RespondError(c, http.StatusInternalServerError, err)
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
			httpshared.RespondError(c, http.StatusInternalServerError, err)
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
			httpshared.RespondError(c, http.StatusInternalServerError, err)
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
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"affected": n})
	}
}
