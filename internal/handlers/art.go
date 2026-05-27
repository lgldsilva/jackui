package handlers

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

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
	// Tracks in-flight frame-capture jobs (the one slow step) per info_hash so a
	// repeated resolve doesn't spawn duplicate ffmpeg runs for the same torrent.
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

		// Build the search query once (shared by TMDB + web). Prefer the cached
		// torrent name; fall back to ?name= so a proactive resolve from a card
		// (where the torrent isn't active and metadata may not be cached) still
		// has something to search. An AI chain cleans the messy release name into
		// a real title first — it beats regex stripping on tricky / non-English
		// names and gives the web search a cleaner query too.
		rawName := ""
		if meta := cache.Get(hash); meta != nil {
			rawName = meta.Name
		}
		if rawName == "" {
			rawName = c.Query("name")
		}
		query := rawName
		if aiClient != nil && rawName != "" {
			if res, _, aerr := aiClient.IdentifyTitle(ctx, rawName); aerr == nil && res.Query() != "" {
				query = res.Query()
			}
		}

		// 2) TMDB poster by title.
		if existingRank < streamer.ArtSourceRank("tmdb") && tmdbClient != nil && query != "" {
			if m, merr := tmdbClient.Match(ctx, query); merr == nil && m != nil && m.PosterURL != "" {
				_ = cache.SetArt(hash, &streamer.CachedArt{
					Source:    "tmdb",
					PosterURL: m.PosterURL,
					TmdbID:    m.TmdbID,
					ImdbID:    m.ImdbID,
				})
				c.JSON(http.StatusOK, gin.H{"source": "tmdb", "tmdbId": m.TmdbID, "imdbId": m.ImdbID})
				return
			}
		}

		// 3) Web image search — only reached when TMDB didn't match (adult /
		// obscure / non-catalogued). Downloads the found image and caches it
		// like a frame so the card serves bytes without re-fetching.
		if existingRank < streamer.ArtSourceRank("web") && webSearch != nil && query != "" {
			if data, _, src, werr := webSearch.Find(ctx, query); werr == nil && len(data) > 0 {
				if rel, serr := s.SaveArtBytes(h, data); serr == nil {
					_ = cache.SetArt(hash, &streamer.CachedArt{Source: "web", Path: rel})
					c.JSON(http.StatusOK, gin.H{"source": "web", "via": src})
					return
				}
			}
		}

		// 4) Captured video frame — the one slow step (ffmpeg can block up to ~25s
		// waiting on pieces). Run it OFF the request: return 202 immediately and
		// extract in the background, persisting when done. The caller (player, on
		// play) is fire-and-forget, and cards re-fetch the art on their next
		// render — so nothing needs to block on this. Deduped per info_hash.
		if fileIdx >= 0 && existingRank < streamer.ArtSourceRank("frame") {
			if _, busy := frameJobs.LoadOrStore(hash, true); busy {
				c.JSON(http.StatusAccepted, gin.H{"source": "frame", "status": "processing"})
				return
			}
			go func() {
				defer frameJobs.Delete(hash)
				bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				for _, at := range frameCaptureSeconds {
					data, _, ferr := s.ExtractThumbnail(bg, h, fileIdx, at)
					if ferr != nil {
						return // torrent gone / index bad
					}
					if len(data) == 0 {
						continue // couldn't decode here; try earlier
					}
					if rel, serr := s.SaveArtBytes(h, data); serr == nil {
						_ = cache.SetArt(hash, &streamer.CachedArt{Source: "frame", Path: rel})
					}
					return
				}
			}()
			c.JSON(http.StatusAccepted, gin.H{"source": "frame", "status": "processing"})
			return
		}

		c.Status(http.StatusNoContent)
	}
}
