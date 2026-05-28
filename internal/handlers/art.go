package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

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
func ResolveArt(s *streamer.Streamer, tmdbClient *tmdb.Client) gin.HandlerFunc {
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
		// Already have the best possible source — never reprocess.
		if existingRank >= streamer.ArtSourceRank("torrent") {
			c.JSON(http.StatusOK, gin.H{"source": existing.Source, "reused": true})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
		defer cancel()

		// 1) Embedded torrent image — uploader-curated, highest trust.
		if existingRank < streamer.ArtSourceRank("torrent") {
			if data, _, terr := s.TorrentImage(ctx, h); terr == nil && len(data) > 0 {
				if rel, serr := s.SaveArtBytes(h, data); serr == nil {
					art := &streamer.CachedArt{Source: "torrent", Path: rel}
					if existing != nil {
						art.TmdbID, art.ImdbID = existing.TmdbID, existing.ImdbID
					}
					_ = cache.SetArt(hash, art)
					c.JSON(http.StatusOK, gin.H{"source": "torrent"})
					return
				}
			}
		}

		// 2) TMDB poster by cached torrent name.
		if existingRank < streamer.ArtSourceRank("tmdb") && tmdbClient != nil {
			if meta := cache.Get(hash); meta != nil && meta.Name != "" {
				if m, merr := tmdbClient.Match(ctx, meta.Name); merr == nil && m != nil && m.PosterURL != "" {
					_ = cache.SetArt(hash, &streamer.CachedArt{
						Source:    "tmdb",
						PosterURL: m.PosterURL,
						TmdbID:    m.TmdbID,
					})
					c.JSON(http.StatusOK, gin.H{"source": "tmdb", "tmdbId": m.TmdbID})
					return
				}
			}
		}

		// 3) Captured video frame — always available once playing.
		if fileIdx >= 0 && existingRank < streamer.ArtSourceRank("frame") {
			for _, at := range frameCaptureSeconds {
				data, _, ferr := s.ExtractThumbnail(ctx, h, fileIdx, at)
				if ferr != nil {
					break // torrent gone / index bad — stop trying timestamps
				}
				if len(data) == 0 {
					continue // couldn't decode at this point; try earlier
				}
				if rel, serr := s.SaveArtBytes(h, data); serr == nil {
					_ = cache.SetArt(hash, &streamer.CachedArt{Source: "frame", Path: rel})
					c.JSON(http.StatusOK, gin.H{"source": "frame"})
					return
				}
				break
			}
		}

		c.Status(http.StatusNoContent)
	}
}
