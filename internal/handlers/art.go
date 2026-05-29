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
			c.Header(CacheControl, CachePublicDay)
			c.Data(http.StatusOK, MIMEJPEG, data)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// frameCaptureSeconds are the timestamps (in priority order) we try when
// grabbing a representative frame — past the typical intro/black first, then
// progressively earlier for short clips.
var frameCaptureSeconds = []int{120, 60, 30, 5}

type artResolveCtx struct {
	c            *gin.Context
	s            *streamer.Streamer
	cache        *streamer.MetadataCache
	tmdbClient   *tmdb.Client
	h            metainfo.Hash
	hash         string
	existingRank int
	ctx          context.Context
	existing     *streamer.CachedArt
	webSearch    *imagesearch.Chain
	isAudio      bool
	rawName      string
	aiClient     *ai.Client
	query        string
	frameJobs    *sync.Map
	fileIdx      int
}

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
		resolveArtHandler(c, s, tmdbClient, aiClient, webSearch, &frameJobs)
	}
}

func resolveArtHandler(c *gin.Context, s *streamer.Streamer, tmdbClient *tmdb.Client, aiClient *ai.Client, webSearch *imagesearch.Chain, frameJobs *sync.Map) {
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

	a := &artResolveCtx{
		c:            c,
		s:            s,
		cache:        cache,
		tmdbClient:   tmdbClient,
		h:            h,
		hash:         hash,
		existing:     existing,
		existingRank: existingRank,
		ctx:          ctx,
		aiClient:     aiClient,
		webSearch:    webSearch,
		frameJobs:    frameJobs,
		fileIdx:      fileIdx,
	}
	buildArtQuery(a)

	if resolveTorrentArt(a) {
		return
	}
	if resolveTMDBArt(a) {
		return
	}
	if resolveWebArt(a) {
		return
	}
	if resolveFrameCapture(a) {
		return
	}

	c.Status(http.StatusNoContent)
}

func buildArtQuery(a *artResolveCtx) {
	if meta := a.cache.Get(a.hash); meta != nil {
		a.rawName = meta.Name
		a.isAudio = isAudioOnlyMeta(meta)
	}
	if a.rawName == "" {
		a.rawName = a.c.Query("name")
	}
	a.query = a.rawName
	if a.aiClient != nil && a.rawName != "" {
		a.query = queryAIIdentify(a.ctx, a.aiClient, a.rawName)
	}
}

func isAudioOnlyMeta(meta *streamer.CachedMeta) bool {
	if len(meta.Files) == 0 {
		return false
	}
	for _, f := range meta.Files {
		if f.IsVideo {
			return false
		}
	}
	return true
}

func queryAIIdentify(ctx context.Context, aiClient *ai.Client, rawName string) string {
	res, _, aerr := aiClient.IdentifyTitle(ctx, rawName)
	if aerr == nil && res.Query() != "" {
		return res.Query()
	}
	return rawName
}

func resolveTorrentArt(a *artResolveCtx) bool {
	if a.existingRank >= streamer.ArtSourceRank("torrent") {
		return false
	}
	data, _, terr := a.s.TorrentImage(a.ctx, a.h)
	if terr != nil || len(data) == 0 {
		return false
	}
	rel, serr := a.s.SaveArtBytes(a.h, data)
	if serr != nil {
		return false
	}
	art := &streamer.CachedArt{Source: "torrent", Path: rel}
	if a.existing != nil {
		art.TmdbID, art.ImdbID = a.existing.TmdbID, a.existing.ImdbID
	}
	_ = a.cache.SetArt(a.hash, art)
	a.c.JSON(http.StatusOK, gin.H{"source": "torrent"})
	return true
}

func resolveTMDBArt(a *artResolveCtx) bool {
	if a.existingRank >= streamer.ArtSourceRank("tmdb") || a.tmdbClient == nil || a.query == "" {
		return false
	}
	m, merr := a.tmdbClient.Match(a.ctx, a.query)
	if merr != nil || m == nil || m.PosterURL == "" {
		return false
	}
	_ = a.cache.SetArt(a.hash, &streamer.CachedArt{
		Source:    "tmdb",
		PosterURL: m.PosterURL,
		TmdbID:    m.TmdbID,
		ImdbID:    m.ImdbID,
	})
	a.c.JSON(http.StatusOK, gin.H{"source": "tmdb", "tmdbId": m.TmdbID, "imdbId": m.ImdbID})
	return true
}

func resolveWebArt(a *artResolveCtx) bool {
	if a.existingRank >= streamer.ArtSourceRank("web") || a.webSearch == nil {
		return false
	}
	webQuery := a.query
	if a.isAudio && a.aiClient != nil && a.rawName != "" {
		if mq := a.aiClient.MusicQuery(a.ctx, a.rawName); mq != "" {
			webQuery = mq
		}
	}
	if webQuery == "" {
		return false
	}
	data, _, src, werr := a.webSearch.Find(a.ctx, webQuery)
	if werr != nil || len(data) == 0 {
		return false
	}
	rel, serr := a.s.SaveArtBytes(a.h, data)
	if serr != nil {
		return false
	}
	_ = a.cache.SetArt(a.hash, &streamer.CachedArt{Source: "web", Path: rel})
	a.c.JSON(http.StatusOK, gin.H{"source": "web", "via": src})
	return true
}

func resolveFrameCapture(a *artResolveCtx) bool {
	if a.fileIdx < 0 || a.existingRank >= streamer.ArtSourceRank("frame") {
		return false
	}
	if _, busy := a.frameJobs.LoadOrStore(a.hash, true); busy {
		a.c.JSON(http.StatusAccepted, gin.H{"source": "frame", "status": "processing"})
		return true
	}
	go func() {
		defer a.frameJobs.Delete(a.hash)
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, at := range frameCaptureSeconds {
			data, _, ferr := a.s.ExtractThumbnail(bg, a.h, a.fileIdx, at)
			if ferr != nil {
				return
			}
			if len(data) == 0 {
				continue
			}
			if rel, serr := a.s.SaveArtBytes(a.h, data); serr == nil {
				_ = a.cache.SetArt(a.hash, &streamer.CachedArt{Source: "frame", Path: rel})
			}
			return
		}
	}()
	a.c.JSON(http.StatusAccepted, gin.H{"source": "frame", "status": "processing"})
	return true
}
