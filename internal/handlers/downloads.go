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
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/streamer"
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

// enrichETA populates DownRate and ETA for a download by looking up the
// active torrent info from the streamer. No-op when streamer is nil.
func enrichETA(d *downloads.Download, s *streamer.Streamer) {
	if s == nil || d.InfoHash == "" || d.FileSize <= 0 {
		return
	}
	var h metainfo.Hash
	if err := h.FromHexString(d.InfoHash); err != nil {
		return
	}
	info, err := s.Get(h)
	if err != nil || info == nil {
		return
	}
	d.DownRate = info.DownRate
	if info.DownRate > 0 {
		remaining := d.FileSize - d.BytesDownloaded
		if remaining > 0 {
			d.ETA = int(remaining / info.DownRate)
		}
	}
}

// enrichETAList calls enrichETA for each download in the slice.
func enrichETAList(list []downloads.Download, s *streamer.Streamer) {
	if s == nil {
		return
	}
	for i := range list {
		enrichETA(&list[i], s)
	}
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
func DownloadsList(store *downloads.Store, streamer *streamer.Streamer, downloadDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _, _ := auth.UserIDFromCtx(c)
		list, err := store.List(userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		enrichETAList(list, streamer)
		markPromoted(list, downloadDir)
		downloads.AssignQueuePositions(list)
		c.JSON(http.StatusOK, list)
	}
}

// DownloadsListFiltered handles GET /api/downloads/filtered — returns
// downloads filtered by query params: status, tracker, category, search,
// sort, order. Also returns available trackers/categories for filter UI.
func DownloadsListFiltered(store *downloads.Store, streamer *streamer.Streamer) gin.HandlerFunc {
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
		enrichETAList(list, streamer)
		downloads.AssignQueuePositions(list)
		c.JSON(http.StatusOK, list)
	}
}

// DownloadsListAll handles GET /api/downloads/all — admin-only: returns
// downloads from ALL users, enriched with usernames. Supports the same
// filtering params as DownloadsListFiltered, plus userId filter.
func DownloadsListAll(dlStore *downloads.Store, authStore *auth.Store, streamer *streamer.Streamer) gin.HandlerFunc {
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
func DownloadsCreate(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			InfoHash  string `json:"infoHash"`
			FileIndex int    `json:"fileIndex"`
			Magnet    string `json:"magnet"`
			Name      string `json:"name"`
			FilePath  string `json:"filePath"`
			FileSize  int64  `json:"fileSize"`
			Tracker   string `json:"tracker,omitempty"`
			Category  string `json:"category,omitempty"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.InfoHash == "" || req.Magnet == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "infoHash and magnet are required"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		d, err := store.Create(downloads.Download{
			UserID:    userID,
			InfoHash:  req.InfoHash,
			FileIndex: req.FileIndex,
			FilePath:  req.FilePath,
			FileSize:  req.FileSize,
			Name:      req.Name,
			Magnet:    req.Magnet,
			Tracker:   req.Tracker,
			Category:  req.Category,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, d)
	}
}

// DownloadsDelete handles DELETE /api/downloads/:id — cancel + remove row.
func DownloadsDelete(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if err := store.Delete(userID, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
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
func DownloadsBatchDelete(store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			IDs []int `json:"ids"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		deleted := 0
		for _, id := range req.IDs {
			if err := store.Delete(userID, id); err == nil {
				deleted++
			}
		}
		c.JSON(http.StatusOK, gin.H{"deleted": deleted, "total": len(req.IDs)})
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
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, _ = s.EnsureActive(ctx, d.Magnet)
			cancel()
		}
		if err := s.RecheckFile(h, d.FileIndex); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		// Reset row pro worker reconciliar com o real após o hash check. Vai pra
		// fila (não direto pra downloading) pro scheduler respeitar o limite.
		_ = store.UpdateProgress(userID, id, 0)
		_ = store.SetStatus(userID, id, downloads.StatusQueued)
		updated, _ := store.Get(userID, id)
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
