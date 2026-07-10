package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
)

// StreamProbe handles GET /api/stream/probe/:hash/:file — runs ffprobe on the
// selected file and returns codec/resolution/duration metadata.
func StreamProbe(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		fileIdx, ok := bindFileIndex(c, "file")
		if !ok {
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
		h, ok := bindHash(c)
		if !ok {
			return
		}
		fileIdx, ok := bindFileIndex(c, "file")
		if !ok {
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
		h, ok := bindHash(c)
		if !ok {
			return
		}
		fileIdx, ok := bindFileIndex(c, "file")
		if !ok {
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
			c.Header(httpshared.ContentType, "text/plain; charset=utf-8")
			c.Header(httpshared.CacheControl, httpshared.CacheImmutable)
			// #nosec G104 -- erro best-effort nao-acionavel
			c.Writer.Write(raw)
			return
		}
		c.Header(httpshared.ContentType, httpshared.MIMEVTT)
		c.Header(httpshared.CacheControl, httpshared.CacheImmutable)
		// #nosec G104 -- erro best-effort nao-acionavel
		c.Writer.Write(body)
	}
}

// StreamSubtitleExtract handles GET /api/stream/subtrack/:hash/:file/:track — extracts an embedded sub as VTT.
func StreamSubtitleExtract(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		fileIdx, ok := bindFileIndex(c, "file")
		if !ok {
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
		c.Header(httpshared.ContentType, httpshared.MIMEVTT)
		c.Header(httpshared.CacheControl, "public, max-age=3600")
		// #nosec G104 -- erro best-effort nao-acionavel
		c.Writer.Write(vtt)
	}
}

// StreamThumbnail handles GET /api/stream/thumb/:hash/:file?at=NNN — returns
// a single JPEG frame captured `at` seconds into the file. Used by the player
// progress-bar hover preview. The path quantizes `at` to 10s buckets so hovering
// across the bar reuses cached thumbs instead of running ffmpeg per pixel.
func StreamThumbnail(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		fileIdx, ok := bindFileIndex(c, "file")
		if !ok {
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
		c.Header(httpshared.CacheControl, httpshared.CachePublicDay) // 1d browser cache
		c.Data(http.StatusOK, httpshared.MIMEJPEG, data)
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
		h, ok := bindHash(c)
		if !ok {
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
		c.Header(httpshared.CacheControl, httpshared.CachePublicDay) // 1d browser cache
		c.JSON(http.StatusOK, meta)
	}
}

// metadataBatchNormalize dedupes valid info_hashes and maps hex → the client's raw key.
func metadataBatchNormalize(hashes []string) (normalized []string, keyForRaw map[string]string) {
	keyForRaw = make(map[string]string, len(hashes))
	for _, raw := range hashes {
		h, err := parseHash(raw)
		if err != nil {
			continue
		}
		hex := h.HexString()
		if _, seen := keyForRaw[hex]; seen {
			continue
		}
		keyForRaw[hex] = raw
		normalized = append(normalized, hex)
	}
	return normalized, keyForRaw
}

func metadataBatchResults(batch map[string]*streamer.CachedMeta, keyForRaw map[string]string) map[string]*streamer.CachedMeta {
	results := make(map[string]*streamer.CachedMeta, len(batch))
	for hex, meta := range batch {
		raw := keyForRaw[hex]
		if raw == "" {
			raw = hex
		}
		results[raw] = meta
	}
	return results
}

// StreamMetadataBatch handles POST /api/stream/metadata/batch {hashes:[...]} →
// {results:{hash:CachedMeta}} — peek-only warm-cache for MANY torrents in ONE
// call so playlist/track lists resolve every cached item with a single
// round-trip instead of one GET /stream/metadata/:hash per group (the N+1 in
// usePlaylistTracks). Cache misses are omitted; the caller falls through to
// streamAdd for cold hashes only.
func StreamMetadataBatch(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Hashes []string `json:"hashes"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || len(req.Hashes) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "hashes is required"})
			return
		}
		if len(req.Hashes) > 500 {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "too many hashes"})
			return
		}
		cache := s.MetadataCache()
		if cache == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "metadata cache disabled"})
			return
		}
		normalized, keyForRaw := metadataBatchNormalize(req.Hashes)
		results := metadataBatchResults(cache.GetBatch(normalized), keyForRaw)
		c.Header(httpshared.CacheControl, httpshared.CachePublicDay)
		c.JSON(http.StatusOK, gin.H{"results": results})
	}
}

// StreamArtwork handles GET /api/stream/artwork/:hash/:file — extracts the
// embedded cover-art image (APIC/PICTURE/covr) from an audio file via ffmpeg
// and serves it with aggressive caching. Returns 204 if no artwork is embedded.
func StreamArtwork(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		fileIdx, ok := bindFileIndex(c, "file")
		if !ok {
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
		c.Header(httpshared.CacheControl, "public, max-age=2592000, immutable") // 30d
		c.Data(http.StatusOK, httpshared.MIMEJPEG, data)
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
		h, ok := bindHash(c)
		if !ok {
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

// StreamHealthBatch handles POST /api/stream/health/batch {hashes:[...]} →
// {results:{hash:health}} — the PEEK (cheap snapshot read) for MANY torrents in
// ONE call, so a list page resolves every SeedBadge with a single round-trip
// instead of one GET /stream/health/:hash per card (the frontend N+1). Peek-only
// (never probes): the expensive swarm probe stays on-demand via the single GET
// with probe=1. The per-hash shape matches the single peek so the SeedBadge
// consumes both identically.
func StreamHealthBatch(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Hashes []string `json:"hashes"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || len(req.Hashes) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "hashes is required"})
			return
		}
		if len(req.Hashes) > 300 {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "too many hashes"})
			return
		}
		results := make(map[string]gin.H, len(req.Hashes))
		for _, raw := range req.Hashes {
			h, err := parseHash(raw)
			if err != nil {
				continue
			}
			snapshot, active := s.HealthSnapshot(h)
			r := gin.H{"active": active, "refreshing": false, "known": snapshot != nil}
			if snapshot != nil {
				r["seeders"] = snapshot.Seeders
				r["peers"] = snapshot.Peers
				r["available"] = snapshot.Available
				r["checkedAt"] = snapshot.CheckedAt
			}
			results[raw] = r
		}
		c.JSON(http.StatusOK, gin.H{"results": results})
	}
}

// StreamTrackers handles GET /api/stream/trackers/:hash?magnet=... — per-tracker
// swarm sizes via BEP 48 scrape, for the torrent info panel. Returns each
// tracker's host (passkeys are never exposed) with its reported seeders/leechers
// and whether it answered. Synchronous (bounded) — the panel shows a spinner.
func StreamTrackers(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		defer cancel()
		stats := s.TrackerStats(ctx, h, c.Query("magnet"))
		if stats == nil {
			stats = []streamer.TrackerScrape{}
		}
		c.JSON(http.StatusOK, stats)
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
