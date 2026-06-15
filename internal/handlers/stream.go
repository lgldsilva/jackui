package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"strings"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
	"github.com/lgldsilva/jackui/internal/transcode"
)

const errInvalidFileIndex = "invalid file index"

type streamAddReq struct {
	Magnet string `json:"magnet"`
	// Kind is the player's classification ("audio" | "video"), sent by the
	// frontend (detectKind). Persisted so Continue Watching / stats group audio
	// correctly. Empty leaves the column untouched (the Upsert only overwrites
	// kind when non-empty), so an older client doesn't wipe a known value.
	Kind string `json:"kind,omitempty"`
}

// normalizeKind whitelists the player's kind hint to the values the library
// column understands ("audio" | "video"); anything else becomes "" so a bogus
// value can't poison the row (and "" leaves the existing column untouched).
func normalizeKind(k string) string {
	switch k {
	case "audio", "video":
		return k
	}
	return ""
}

// StreamAdd handles POST /api/stream/add — registers a magnet, waits for metadata.
// Side-effect: persists the magnet in the user's library so they can re-play after restart
// or from /favorites without going through a new search.
func StreamAdd(s *streamer.Streamer, lib *library.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req streamAddReq
		if err := c.ShouldBindJSON(&req); err != nil || req.Magnet == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "magnet is required"})
			return
		}
		info, err := s.Add(c.Request.Context(), req.Magnet)
		if err != nil {
			// Log enough context to debug pipeline issues without leaking the full magnet
			preview := req.Magnet
			if len(preview) > 80 {
				preview = preview[:80] + "..."
			}
			fmt.Printf("[stream/add] failed for %q: %v\n", preview, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		// Persist into the user's library (idempotent upsert). Kind comes from the
		// player (detectKind); empty leaves the column untouched.
		// In incognito mode: still upsert so the entry exists for resume tracking,
		// but mark it with incognito=1 so it is excluded from normal listings and
		// deleted when the user ends their incognito session.
		if lib != nil {
			userID, _, _ := auth.UserIDFromCtx(c)
			_, _ = lib.Upsert(library.UpsertInput{UserID: userID, InfoHash: info.InfoHash, Magnet: req.Magnet, Name: info.Name, PrimaryFile: info.PrimaryFile, TotalSize: info.TotalSize, Kind: normalizeKind(req.Kind), Incognito: middleware.IsIncognito(c)})
		}
		c.JSON(http.StatusOK, info)
	}
}

// StreamAddTorrentFile handles POST /api/stream/add-file — adds a torrent from uploaded .torrent file.
func StreamAddTorrentFile(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		file, err := c.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
			return
		}
		src, err := file.Open()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer func() { _ = src.Close() }()

		mi, err := metainfo.Load(src)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid torrent file: " + err.Error()})
			return
		}

		t, err := s.Client().AddTorrent(mi)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add torrent: " + err.Error()})
			return
		}

		// Wait for metadata
		select {
		case <-t.GotInfo():
		default:
		}

		m, merr := mi.MagnetV2()
		if merr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": merr.Error()})
			return
		}
		magnet := m.String()
		info, err := s.Add(c.Request.Context(), magnet)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, info)
	}
}

// StreamInfo handles GET /api/stream/info/:hash — current torrent state + progress.
func StreamInfo(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		info, err := s.Get(h)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, info)
	}
}

func StreamFile(s *streamer.Streamer, store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}
		if serveFromCompletedStore(c, store, s, h, fileIdx) {
			return
		}
		serveFromStreamer(c, s, h, fileIdx)
	}
}

// serveFromCompletedStore serves a finished download straight from disk. Both
// row shapes are honored: per-file rows point at the file itself; whole-torrent
// rows point at the torrent's directory, so the requested file is located via
// the cached metainfo rel path (no torrent activation, no swarm dependency).
func serveFromCompletedStore(c *gin.Context, store *downloads.Store, s *streamer.Streamer, h metainfo.Hash, fileIdx int) bool {
	if store == nil {
		return false
	}
	path, err := store.GetCompletedPathRel(h.HexString(), fileIdx, s.FileRelPath(h, fileIdx))
	if err != nil || path == "" {
		return false
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return false
	}
	// Same stored-XSS guard as /api/local/file: torrent contents are hostile
	// by default and this endpoint serves them same-origin (JWT in
	// localStorage). HTML/SVG/JS are forced to download instead of rendering.
	setLocalFileSecurityHeaders(c, path)
	http.ServeFile(c.Writer, c.Request, path)
	return true
}

func serveFromStreamer(c *gin.Context, s *streamer.Streamer, h metainfo.Hash, fileIdx int) {
	reader, file, err := s.FileReader(h, fileIdx)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = reader.Close() }()
	setLocalFileSecurityHeaders(c, file.DisplayPath())
	http.ServeContent(c.Writer, c.Request, file.DisplayPath(), time.Time{}, reader)
}

// StreamPlaylistM3U handles GET /api/stream/playlist/:hash/:file.m3u — returns a
// small M3U playlist file that points back to the stream URL. Used by the "VLC"
// button: browsers download the .m3u, the OS opens it in the registered M3U
// handler (VLC on every desktop and iOS/Android with VLC installed).
//
// This is universally portable, unlike vlc:// or vlc-x-callback:// URL schemes
// which only work on subsets of devices and break on desktop VLC entirely.
func StreamPlaylistM3U(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}

		info, err := resolveTorrentInfo(s, c.Request.Context(), h)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if fileIdx < 0 || fileIdx >= len(info.Files) {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrFileIdxOutOfRange})
			return
		}

		streamURL := buildStreamURL(c, h, fileIdx)
		title := info.Files[fileIdx].Path
		if title == "" {
			title = info.Name
		}
		m3u := "#EXTM3U\n" +
			"#EXTINF:-1," + title + "\n" +
			streamURL + "\n"

		filename := m3uFilename(info, fileIdx)
		c.Header(HeaderContentDisp, fmt.Sprintf(`attachment; filename="%s.m3u"`, filename))
		c.Data(http.StatusOK, "audio/x-mpegurl", []byte(m3u))
	}
}

// resolveTorrentInfo returns the TorrentInfo for the given hash. If the torrent
// is not currently active, it attempts a best-effort auto-add using a bare
// magnet so that VLC links survive cache evictions and container restarts.
func resolveTorrentInfo(s *streamer.Streamer, ctx context.Context, h metainfo.Hash) (*streamer.TorrentInfo, error) {
	info, err := s.Get(h)
	if err != nil {
		bareMagnet := MagnetPrefix + h.HexString()
		got, addErr := s.Add(ctx, bareMagnet)
		if addErr != nil {
			return nil, err
		}
		info = got
	}
	return info, nil
}

// buildStreamURL builds the absolute stream URL using the same scheme/host the
// client used to reach us, propagating auth token and optional ?transcode param.
func buildStreamURL(c *gin.Context, h metainfo.Hash, fileIdx int) string {
	scheme := "http"
	if c.Request.TLS != nil || c.Request.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := c.Request.Host

	token := ""
	if h := c.Request.Header.Get(HeaderAuthorization); strings.HasPrefix(h, auth.BearerPrefix) {
		token = strings.TrimPrefix(h, auth.BearerPrefix)
	}
	if token == "" {
		token = c.Query("token")
	}

	path := fmt.Sprintf("/api/stream/%s/%d", h.HexString(), fileIdx)
	if v := c.Query("transcode"); v == "h264" || v == "hevc" {
		path = fmt.Sprintf("/api/stream/transcode/%s/%d?video=%s", h.HexString(), fileIdx, v)
	}

	base := fmt.Sprintf("%s://%s%s", scheme, host, path)
	if token == "" {
		return base
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "token=" + token
}

// m3uFilename builds a filesystem-safe filename derived from the playing
// file's basename. Example: "Season 1/S01E01 - Pilot.mkv" → "S01E01 - Pilot".
func m3uFilename(info *streamer.TorrentInfo, fileIdx int) string {
	base := info.Files[fileIdx].Path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if dot := strings.LastIndex(base, "."); dot > 0 {
		base = base[:dot]
	}
	safe := strings.NewReplacer(`"`, "", "\r", "", "\n", "", "\\", "").Replace(base)
	if safe == "" {
		safe = "stream"
	}
	return safe
}

// StreamPrefetch handles POST /api/stream/prefetch/:hash/:file — best-effort
// background fetch of a file that is NOT being streamed right now. Used by the
// player to warm up the next episode (or next playlist item, when same torrent)
// at ~50% of the current item so the transition is seamless.
//
// Returns 202 immediately; the actual piece download happens asynchronously.
func StreamPrefetch(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
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
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		s.Drop(h)
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
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if s.ReleaseViewer(h) && hlsMgr != nil {
			hlsMgr.CloseForHash(h.HexString())
		}
		c.JSON(http.StatusOK, gin.H{"message": "released"})
	}
}

// StreamProbe handles GET /api/stream/probe/:hash/:file — lists embedded audio + sub tracks.
func StreamProbe(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}
		// ffprobe is bounded; 60s is generous
		ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
		defer cancel()
		probe, err := s.Probe(ctx, h, fileIdx)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, probe)
	}
}

// StreamSidecars handles GET /api/stream/sidecars/:hash/:file — list .srt/.vtt/.ass sibling files in the torrent.
func StreamSidecars(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}
		subs, err := s.Sidecars(h, fileIdx)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, subs)
	}
}

// StreamSidecarRead handles GET /api/stream/sidecar/:hash/:file — reads one sidecar file as WebVTT.
// :file is the absolute torrent file index (from `streamer.Sidecars().Index`).
// Converts SRT → VTT automatically; serves VTT as-is.
func StreamSidecarRead(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
		defer cancel()
		raw, format, err := s.ReadSidecar(ctx, h, fileIdx)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		var body []byte
		switch strings.ToLower(format) {
		case "srt":
			body = subtitles.SRTToVTT(raw)
		case "vtt":
			body = raw
		default:
			// ASS/SSA need ffmpeg to convert — for now, just serve raw with text/plain so browsers can show it as "non-VTT"
			c.Header(ContentType, "text/plain; charset=utf-8")
			c.Header(CacheControl, CacheImmutable)
			c.Writer.Write(raw)
			return
		}
		c.Header(ContentType, MIMEVTT)
		c.Header(CacheControl, CacheImmutable)
		c.Writer.Write(body)
	}
}

// StreamSubtitleExtract handles GET /api/stream/subtrack/:hash/:file/:track — extracts an embedded sub as VTT.
func StreamSubtitleExtract(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}
		trackIdx, err := strconv.Atoi(c.Param("track"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid track index"})
			return
		}
		// Sub extraction can be slow on a fresh stream because MKV interleaves sub data
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
		defer cancel()
		vtt, err := s.ExtractSubtitle(ctx, h, fileIdx, trackIdx)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.Header(ContentType, MIMEVTT)
		c.Header(CacheControl, "public, max-age=3600")
		c.Writer.Write(vtt)
	}
}

// StreamThumbnail handles GET /api/stream/thumb/:hash/:file?at=NNN — returns
// a single JPEG frame captured `at` seconds into the file. Used by the player
// progress-bar hover preview. The path quantizes `at` to 10s buckets so hovering
// across the bar reuses cached thumbs instead of running ffmpeg per pixel.
func StreamThumbnail(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}
		at, _ := strconv.Atoi(c.Query("at"))
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		data, _, err := s.ExtractThumbnail(ctx, h, fileIdx, at)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if len(data) == 0 {
			c.Status(http.StatusNoContent)
			return
		}
		c.Header(CacheControl, CachePublicDay) // 1d browser cache
		c.Data(http.StatusOK, MIMEJPEG, data)
	}
}

// StreamMetadata handles GET /api/stream/metadata/:hash — returns a cached
// snapshot of TorrentInfo without requiring the torrent to be active. Lets the
// UI render the file list + name *instantly* on subsequent opens, while the
// (slower) streamAdd kicks off in parallel to actually start downloading.
//
// 200 = cache hit, 404 = never seen this hash before.
func StreamMetadata(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		cache := s.MetadataCache()
		if cache == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "metadata cache disabled"})
			return
		}
		meta := cache.Get(h.HexString())
		if meta == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "no cached metadata"})
			return
		}
		c.Header(CacheControl, CachePublicDay) // 1d browser cache
		c.JSON(http.StatusOK, meta)
	}
}

// StreamArtwork handles GET /api/stream/artwork/:hash/:file — extracts the
// embedded cover-art image (APIC/PICTURE/covr) from an audio file via ffmpeg
// and serves it with aggressive caching. Returns 204 if no artwork is embedded.
func StreamArtwork(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		data, _, err := s.ExtractArtwork(ctx, h, fileIdx)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if len(data) == 0 {
			c.Status(http.StatusNoContent)
			return
		}
		c.Header(CacheControl, "public, max-age=2592000, immutable") // 30d
		c.Data(http.StatusOK, MIMEJPEG, data)
	}
}

// StreamHealth handles GET /api/stream/health/:hash?magnet=...&probe=1 — returns
// the last-known swarm health (seeders/peers/available + when it was checked).
//
// PEEK by default (cheap: DB read / live stats only). A swarm probe — which adds
// the torrent to the swarm to count peers — is EXPENSIVE and only runs when the
// caller explicitly asks with probe=1. Auto-probing on every visible card turned
// the whole UI sluggish and spawned phantom "active torrents", so it's now
// strictly on-demand.
func StreamHealth(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		snapshot, active := s.HealthSnapshot(h)
		magnet := c.Query("magnet")
		stale := snapshot == nil || time.Since(snapshot.CheckedAt) > streamer.HealthFreshFor
		// Only probe when explicitly requested AND it'd add value (not active,
		// stale snapshot, and a tracker source exists — magnet tr= or a cached
		// .torrent, the latter covering private results that ship no magnet).
		refreshing := c.Query("probe") == "1" && !active && stale && s.CanProbeHealth(h, magnet)
		if refreshing {
			s.ProbeHealthAsync(h, magnet)
		}
		resp := gin.H{"active": active, "refreshing": refreshing, "known": snapshot != nil}
		if snapshot != nil {
			resp["seeders"] = snapshot.Seeders
			resp["peers"] = snapshot.Peers
			resp["available"] = snapshot.Available
			resp["checkedAt"] = snapshot.CheckedAt
		}
		c.JSON(http.StatusOK, resp)
	}
}

// StreamCacheStats handles GET /api/stream/cache — disk usage of the streaming cache.
func StreamCacheStats(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		stats, err := s.Stats()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, stats)
	}
}

// StreamRateStats handles GET /api/stream/rate — aggregate DL/UL bytes/sec
// across all active torrents. The frontend polls this every 2s for the header widget.
func StreamRateStats(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, s.GlobalStats())
	}
}

// StreamCacheClear handles DELETE /api/stream/cache — wipe everything.
// DELETE /api/stream/cache?entry=<name> removes one entry only.
func StreamCacheClear(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		if entry := c.Query("entry"); entry != "" {
			if err := s.ClearEntry(entry); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"message": "entry cleared"})
			return
		}
		if err := s.ClearAll(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "cache cleared"})
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
		includeAll := isAdmin && c.Query("all") == "1"
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
		includeAll := isAdmin && c.Query("all") == "1"
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

func parseHash(s string) (metainfo.Hash, error) {
	var h metainfo.Hash
	return h, h.FromHexString(s)
}

// ─── Transmission-style download controls ──────────────────────────────────

// StreamPause handles POST /api/stream/:hash/pause — soft-pause peer connections.
func StreamPause(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
			if strings.Contains(err.Error(), "não está ativo") {
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
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
			if strings.Contains(err.Error(), "não está ativo") {
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
