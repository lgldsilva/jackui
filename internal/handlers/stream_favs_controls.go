package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
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
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"status": "prefetching"})
	}
}

// StreamDrop handles DELETE /api/stream/:hash — manually stop a torrent.
// Também encerra as sessões HLS daquele torrent (#17): fechar o player não pode
// deixar o ffmpeg do transcode órfão consumindo CPU até o idle-reaper.
func StreamDrop(s *streamer.Streamer, hlsMgr *transcode.HLSSessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		// DropSeed (não Drop): remover o torrent é uma ação explícita do usuário,
		// então também limpa o auto-seed persistido — senão ele voltaria a seedar
		// no próximo boot e reapareceria como "ativo".
		s.DropSeed(h)
		if hlsMgr != nil {
			hlsMgr.CloseForHash(h.HexString())
		}
		c.JSON(http.StatusOK, gin.H{"message": "dropped"})
	}
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
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrNameRequired})
			return
		}
		if req.Reason == "" {
			req.Reason = "manual"
		}
		favs := s.Favorites()
		if favs == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "favorites store not initialized"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		if err := favs.Add(req.Name, req.InfoHash, req.Magnet, req.Reason, userID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "favorites store not initialized"})
			return
		}
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && queryBool(c, "all")
		if err := favs.Remove(name, userID, includeAll); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := s.SetPriority(h, req.Priority); err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, streamer.ErrTorrentNotActive) {
				code = http.StatusNotFound
			}
			c.JSON(code, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}
		var req struct {
			Priority string `json:"priority"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := s.SetFilePriority(h, idx, req.Priority); err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, streamer.ErrTorrentNotActive) {
				code = http.StatusNotFound
			}
			c.JSON(code, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"priority": strings.ToLower(req.Priority)})
	}
}

// StreamActive handles GET /api/stream/active — snapshot of every active torrent.
func StreamActive(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		list := s.ActiveList()
		if list == nil {
			list = []*streamer.TorrentInfo{}
		}
		c.JSON(http.StatusOK, list)
	}
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
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.Down < 0 || req.Up < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limits must be >= 0 (0 = unlimited)"})
			return
		}
		s.SetRateLimits(req.Down, req.Up)
		c.JSON(http.StatusOK, gin.H{"down": req.Down, "up": req.Up})
	}
}
