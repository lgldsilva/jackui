package handlers

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/luizg/jackui/internal/ai"
	"github.com/luizg/jackui/internal/imagesearch"
	"github.com/luizg/jackui/internal/streamer"
	"github.com/luizg/jackui/internal/tmdb"
)

// StreamArt handles GET /api/stream/art/:hash — serves the persisted thumbnail
// for a torrent. This path is intentionally CHEAP (a single DB read + either a
// redirect or a disk read): it's hit once per card across long lists, so it must
// never block on the swarm, ffmpeg, or the network. Resolution of *new* art
// (which can be slow) happens out-of-band in ResolveArt, triggered on play.
//
//	302 → TMDB poster (source=tmdb; browser loads from the CDN)
//	200 → JPEG bytes (source=torrent/frame, served from the .art cache)
//	204 → no art resolved yet (frontend falls back to title-based TMDB)
func StreamArt(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		cache := s.MetadataCache()
		if cache == nil {
			c.Status(http.StatusNoContent)
			return
		}
		art := cache.GetArt(h.HexString())
		if art == nil {
			c.Status(http.StatusNoContent)
			return
		}
		if art.Source == "tmdb" && art.PosterURL != "" {
			// 302 keeps the heavy image bytes off our server and lets the browser
			// cache them against the CDN URL.
			c.Redirect(http.StatusFound, art.PosterURL)
			return
		}
		if art.Path != "" {
			data, rerr := s.ReadArtBytes(art.Path)
			if rerr != nil || len(data) == 0 {
				c.Status(http.StatusNoContent)
				return
			}
			c.Header("Cache-Control", "public, max-age=86400")
			c.Data(http.StatusOK, "image/jpeg", data)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// frameCaptureSeconds are the timestamps (in priority order) we try when
// grabbing a representative frame — past the typical intro/black first, then
// progressively earlier for short clips.
var frameCaptureSeconds = []int{120, 60, 30, 5}

// ResolveArt handles POST /api/stream/art/:hash/resolve?file=N — runs the art
// resolution chain and persists the best result keyed by info_hash, so it's
// never recomputed. Triggered by the player on play. Idempotent: if a result of
// equal-or-higher rank is already persisted it returns immediately without
// touching the swarm, TMDB, or ffmpeg.
//
// Chain (highest trust first): embedded torrent image → TMDB poster (by cached
// name) → captured video frame.
func ResolveArt(s *streamer.Streamer, tmdbClient *tmdb.Client, aiClient *ai.Client, webSearch *imagesearch.Chain) gin.HandlerFunc {
	var frameJobs sync.Map
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
		hash := h.HexString()
		fileIdx, _ := strconv.Atoi(c.DefaultQuery("file", "-1"))

		existing := cache.GetArt(hash)
		existingRank := 0
		if existing != nil {
			existingRank = streamer.ArtSourceRank(existing.Source)
		}
		if existingRank >= streamer.ArtSourceRank("torrent") {
			c.JSON(http.StatusOK, gin.H{"source": existing.Source, "reused": true})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
		defer cancel()

		rawName, isAudio, query := buildArtQuery(c, cache, aiClient, ctx, hash)

		if resolveTorrentArt(c, s, cache, h, hash, existing, existingRank, ctx) {
			return
		}
		if resolveTMDBArt(c, cache, tmdbClient, hash, existingRank, ctx, query) {
			return
		}
		if resolveWebArt(c, s, cache, h, hash, webSearch, existingRank, ctx, isAudio, rawName, aiClient, query) {
			return
		}
		if resolveFrameCapture(c, s, cache, h, hash, &frameJobs, fileIdx, existingRank) {
			return
		}

		c.Status(http.StatusNoContent)
	}
}

func buildArtQuery(c *gin.Context, cache *streamer.MetadataCache, aiClient *ai.Client, ctx context.Context, hash string) (rawName string, isAudio bool, query string) {
	if meta := cache.Get(hash); meta != nil {
		rawName = meta.Name
		if len(meta.Files) > 0 {
			isAudio = true
			for _, f := range meta.Files {
				if f.IsVideo {
					isAudio = false
					break
				}
			}
		}
	}
	if rawName == "" {
		rawName = c.Query("name")
	}
	query = rawName
	if aiClient != nil && rawName != "" {
		if res, _, aerr := aiClient.IdentifyTitle(ctx, rawName); aerr == nil && res.Query() != "" {
			query = res.Query()
		}
	}
	return
}

func resolveTorrentArt(c *gin.Context, s *streamer.Streamer, cache *streamer.MetadataCache, h metainfo.Hash, hash string, existing *streamer.CachedArt, existingRank int, ctx context.Context) bool {
	if existingRank >= streamer.ArtSourceRank("torrent") {
		return false
	}
	data, _, terr := s.TorrentImage(ctx, h)
	if terr != nil || len(data) == 0 {
		return false
	}
	rel, serr := s.SaveArtBytes(h, data)
	if serr != nil {
		return false
	}
	art := &streamer.CachedArt{Source: "torrent", Path: rel}
	if existing != nil {
		art.TmdbID, art.ImdbID = existing.TmdbID, existing.ImdbID
	}
	_ = cache.SetArt(hash, art)
	c.JSON(http.StatusOK, gin.H{"source": "torrent"})
	return true
}

func resolveTMDBArt(c *gin.Context, cache *streamer.MetadataCache, tmdbClient *tmdb.Client, hash string, existingRank int, ctx context.Context, query string) bool {
	if existingRank >= streamer.ArtSourceRank("tmdb") || tmdbClient == nil || query == "" {
		return false
	}
	m, merr := tmdbClient.Match(ctx, query)
	if merr != nil || m == nil || m.PosterURL == "" {
		return false
	}
	_ = cache.SetArt(hash, &streamer.CachedArt{
		Source:    "tmdb",
		PosterURL: m.PosterURL,
		TmdbID:    m.TmdbID,
		ImdbID:    m.ImdbID,
	})
	c.JSON(http.StatusOK, gin.H{"source": "tmdb", "tmdbId": m.TmdbID, "imdbId": m.ImdbID})
	return true
}

func resolveWebArt(c *gin.Context, s *streamer.Streamer, cache *streamer.MetadataCache, h metainfo.Hash, hash string, webSearch *imagesearch.Chain, existingRank int, ctx context.Context, isAudio bool, rawName string, aiClient *ai.Client, query string) bool {
	if existingRank >= streamer.ArtSourceRank("web") || webSearch == nil {
		return false
	}
	webQuery := query
	if isAudio && aiClient != nil && rawName != "" {
		if mq := aiClient.MusicQuery(ctx, rawName); mq != "" {
			webQuery = mq
		}
	}
	if webQuery == "" {
		return false
	}
	data, _, src, werr := webSearch.Find(ctx, webQuery)
	if werr != nil || len(data) == 0 {
		return false
	}
	rel, serr := s.SaveArtBytes(h, data)
	if serr != nil {
		return false
	}
	_ = cache.SetArt(hash, &streamer.CachedArt{Source: "web", Path: rel})
	c.JSON(http.StatusOK, gin.H{"source": "web", "via": src})
	return true
}

func resolveFrameCapture(c *gin.Context, s *streamer.Streamer, cache *streamer.MetadataCache, h metainfo.Hash, hash string, frameJobs *sync.Map, fileIdx int, existingRank int) bool {
	if fileIdx < 0 || existingRank >= streamer.ArtSourceRank("frame") {
		return false
	}
	if _, busy := frameJobs.LoadOrStore(hash, true); busy {
		c.JSON(http.StatusAccepted, gin.H{"source": "frame", "status": "processing"})
		return true
	}
	go func() {
		defer frameJobs.Delete(hash)
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, at := range frameCaptureSeconds {
			data, _, ferr := s.ExtractThumbnail(bg, h, fileIdx, at)
			if ferr != nil {
				return
			}
			if len(data) == 0 {
				continue
			}
			if rel, serr := s.SaveArtBytes(h, data); serr == nil {
				_ = cache.SetArt(hash, &streamer.CachedArt{Source: "frame", Path: rel})
			}
			return
		}
	}()
	c.JSON(http.StatusAccepted, gin.H{"source": "frame", "status": "processing"})
	return true
}
