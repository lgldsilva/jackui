package handlers

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

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
// recheckPrepare resolves the download row + infoHash and (re-)attaches the
// torrent so RecheckFile/RecheckAllFiles have access to the files. It writes
// the error response itself and returns ok=false on failure.
func recheckPrepare(c *gin.Context, store *downloads.Store, s *streamer.Streamer, userID, id int) (*downloads.Download, metainfo.Hash, bool) {
	var h metainfo.Hash
	d, err := store.Get(userID, id)
	if err != nil || d == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": errDownloadNotFound})
		return nil, h, false
	}
	if err := h.FromHexString(d.InfoHash); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "infoHash inválido"})
		return nil, h, false
	}
	// EnsureActive antes do recheck — se o torrent foi dropado (ex.: post-
	// completed sem seed), precisa re-attach pra ter acesso aos files.
	if d.Magnet != "" {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		_, err := s.EnsureActive(ctx, d.Magnet)
		cancel()
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return nil, h, false
		}
	}
	return d, h, true
}

func DownloadsRecheck(store *downloads.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrInvalidID})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		d, h, ok := recheckPrepare(c, store, s, userID, id)
		if !ok {
			return
		}
		// Whole-torrent rows re-hash every file; per-file rows only theirs.
		recheck := s.RecheckFile
		if d.IsWholeTorrent() {
			recheck = func(_ metainfo.Hash, _ int) error { return s.RecheckAllFiles(h) }
		}
		if err := recheck(h, d.FileIndex); err != nil {
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
