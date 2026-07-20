package main

import (
	"context"
	"crypto/subtle"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/gluetun"
	"github.com/lgldsilva/jackui/internal/handlers"
	lh "github.com/lgldsilva/jackui/internal/handlers/local"
	"github.com/lgldsilva/jackui/internal/mailer"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transmissionrpc"
	"github.com/lgldsilva/jackui/ui"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func mustGetDistFS() fs.FS {
	distFS, err := fs.Sub(ui.FS, "dist")
	if err != nil {
		log.Fatalf("Failed to create sub filesystem: %v", err)
	}
	return distFS
}

func spaFallback(distFS fs.FS, fileServer http.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		if strings.HasPrefix(path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "endpoint not found"})
			return
		}

		isHashedAsset := strings.HasPrefix(path, "/assets/") ||
			strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") ||
			strings.HasSuffix(path, ".woff2") || strings.HasSuffix(path, ".woff")

		if isHashedAsset {
			c.Writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			c.Writer.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			c.Writer.Header().Set("Pragma", "no-cache")
			c.Writer.Header().Set("Expires", "0")
		}

		f, err := distFS.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			// #nosec G104 -- Close best-effort no cleanup; erro no teardown irrelevante
			f.Close()
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}

// metricsStaticTokenBypass lets a Prometheus scraper authenticate with the
// static JACKUI_METRICS_TOKEN (Bearer header or ?token=) when auth is on.
// On match it serves the metrics and aborts the chain; otherwise it falls
// through to the regular admin-JWT requirement. Constant-time compare.
func metricsStaticTokenBypass(prom gin.HandlerFunc) gin.HandlerFunc {
	static := strings.TrimSpace(os.Getenv("JACKUI_METRICS_TOKEN"))
	return func(c *gin.Context) {
		if static == "" {
			c.Next()
			return
		}
		presented := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if presented == "" || presented == c.GetHeader("Authorization") {
			presented = c.Query("token")
		}
		if subtle.ConstantTimeCompare([]byte(presented), []byte(static)) == 1 {
			prom(c)
			c.Abort()
			return
		}
		c.Next()
	}
}

// peerPortRefreshHandler re-reads gluetun's forwarded port and, if it differs
// from the port the streamer is bound to, signals the graceful restart so
// resolvePeerPort rebinds to the new port on boot. Authenticated by the static
// JACKUI_CONTROL_TOKEN (Bearer header or ?token=), constant-time compared — the
// caller is the VPN port-routing script in the gluetun netns, which can't hold a
// JWT. Disabled (503) when no token is configured; no-op when not behind gluetun.
func peerPortRefreshHandler(ctrlURL string, s *streamer.Streamer, restart chan<- struct{}) gin.HandlerFunc {
	token := strings.TrimSpace(os.Getenv("JACKUI_CONTROL_TOKEN"))
	return func(c *gin.Context) {
		if token == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "JACKUI_CONTROL_TOKEN not configured"})
			return
		}
		presented := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if presented == "" || presented == c.GetHeader("Authorization") {
			presented = c.Query("token")
		}
		if subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		if ctrlURL == "" {
			c.JSON(http.StatusOK, gin.H{"changed": false, "reason": "not behind gluetun"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Second)
		defer cancel()
		p, err := gluetun.ForwardedPort(ctx, ctrlURL)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		cur := 0
		if s != nil {
			cur = s.ListenPort()
		}
		if p == cur {
			c.JSON(http.StatusOK, gin.H{"changed": false, "port": p})
			return
		}
		select {
		case restart <- struct{}{}:
		default:
		}
		c.JSON(http.StatusOK, gin.H{"changed": true, "from": cur, "to": p})
	}
}

func setupRouter(deps *appDeps) *gin.Engine {
	router := gin.New()
	// Custom formatter instead of gin.Logger(): media routes authenticate via
	// ?token=<JWT> (<video> can't send headers), and the default access log was
	// writing those JWTs verbatim into `docker logs`.
	router.Use(gin.LoggerWithFormatter(middleware.RedactingLogFormatter))
	router.Use(gin.Recovery())

	corsConfig := cors.DefaultConfig()
	if origins := deps.cfg.AllowedOrigins; len(origins) > 0 {
		corsConfig.AllowOrigins = origins
		corsConfig.AllowAllOrigins = false
	} else {
		// Sem AllowedOrigins configurado: mantém compatibilidade com o
		// comportamento legado (AllowAllOrigins) para não quebrar deployments
		// existentes, mas avisa que é inseguro em produção. O admin deve definir
		// JACKUI_ALLOWED_ORIGINS ou allowed_origins no config.yaml.
		corsConfig.AllowAllOrigins = true
		log.Print("[SECURITY] CORS: AllowedOrigins vazio — permitindo TODAS as origens. " +
			"Defina JACKUI_ALLOWED_ORIGINS=... no .env ou allowed_origins no config.yaml.")
	}
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization", "Range"}
	corsConfig.ExposeHeaders = []string{"Content-Length", "Content-Range", "Accept-Ranges"}
	router.Use(cors.New(corsConfig))

	router.GET("/healthz", handlers.Health(deps.historyStore, func() bool { return deps.streamSrv != nil }))
	// Public build metadata (commit/build time/version) — checkable without a token.
	router.GET("/status", handlers.BuildInfo(deps.historyStore))
	// Prometheus metrics. With auth enabled the endpoint requires either the
	// static scraper token (JACKUI_METRICS_TOKEN — JWTs expire, scrape configs
	// don't refresh) or an admin JWT; labels could otherwise leak torrent/file
	// names past the auth layer. Without auth it stays open as before.
	promHandler := gin.WrapH(promhttp.Handler())
	if deps.cfg.Auth.Enabled && deps.tokenMgr != nil {
		router.GET("/api/metrics", metricsStaticTokenBypass(promHandler), auth.Required(deps.tokenMgr), auth.AdminOnly(), promHandler)
	} else {
		router.GET("/api/metrics", promHandler)
	}
	// Peer-port refresh: lets the gluetun port-forward up-command push an immediate
	// rebind when ProtonVPN rotates the forwarded port (vs waiting for the ~2min
	// watcher poll). Static-token auth (JACKUI_CONTROL_TOKEN) — the caller is a
	// shell script inside the gluetun netns, not a browser, so it can't carry a JWT.
	router.POST("/api/stream/peer-port/refresh", peerPortRefreshHandler(os.Getenv("JACKUI_GLUETUN_CONTROL_URL"), deps.streamSrv, deps.restart))
	router.GET("/api/auth/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"enabled": deps.cfg.Auth.Enabled})
	})

	// Transmission RPC compatibility — so Sonarr/Radarr/Prowlarr can talk to
	// JackUI as if it were a Transmission daemon. OPT-IN via
	// JACKUI_TRANSMISSION_RPC_ENABLED=1 (default OFF): é uma superfície RPC e,
	// com JACKUI_AUTH_ENABLED desligado, ficaria sem autenticação — habilite só
	// em LAN e/ou com auth ligada. Só registra com streamer/downloads disponíveis.
	if transmissionRPCEnabled() && deps.downloadsStore != nil && deps.streamSrv != nil {
		trpc := transmissionrpc.NewHandler(
			deps.downloadsStore, deps.streamSrv, deps.authStore,
			deps.streamCfg.DataDir, deps.cfg.Stream.DownloadDir,
			deps.cfg.Stream.SharedDir, func() bool { return deps.cfg.DownloadsQueue.AutoPromoteArr },
		)
		trpc.RegisterRoutes(router)
		log.Printf("Transmission RPC: /transmission/rpc (compat layer for *arr stack)")
	}

	log.Printf("Email (SMTP): %s", emailStatus(deps.mlr))

	if deps.authStore != nil && deps.tokenMgr != nil {
		pub := router.Group("/api/auth")
		loginRateLimit := middleware.RateLimit(deps.loginRateLimiter)
		registerRateLimit := middleware.RateLimit(deps.registerRateLimiter)
		passwordRateLimit := middleware.RateLimit(deps.passwordRateLimiter)
		pub.POST("/login", loginRateLimit, handlers.Login(deps.authStore, deps.tokenMgr, deps.loginLockout))
		pub.POST("/refresh", handlers.Refresh(deps.authStore, deps.tokenMgr))
		// Optional so Bearer is parsed when present: Logout must see claims to
		// purge incognito history/library. Without this, logout never cleaned.
		pub.POST("/logout", auth.Optional(deps.tokenMgr), handlers.Logout(deps.authStore, deps.historyStore, deps.libraryStore))
		pub.POST("/register", registerRateLimit, handlers.Register(deps.authStore, deps.mlr, deps.cfg.BaseURL))
		pub.POST("/verify-email", handlers.VerifyEmail(deps.authStore))
		pub.POST("/forgot", passwordRateLimit, handlers.Forgot(deps.authStore, deps.mlr, deps.cfg.BaseURL))
		pub.POST("/reset", passwordRateLimit, handlers.Reset(deps.authStore))
		pub.POST("/passkey/login/begin", handlers.PasskeyLoginBegin(deps.authStore, deps.waManager))
		pub.POST("/passkey/login/finish", handlers.PasskeyLoginFinish(deps.authStore, deps.tokenMgr, deps.waManager))
	}

	api := router.Group("/api")
	if deps.tokenMgr != nil {
		api.Use(auth.Required(deps.tokenMgr))
		api.Use(auth.GuestRestrict())
	}
	api.Use(middleware.Incognito())
	api.Use(middleware.RevealHidden())
	{
		api.GET("/classify", handlers.ClassifyCategory(deps.aiClient))
		api.POST("/diag/log", handlers.ClientLog())
		api.GET("/search", handlers.Search(deps.jackettClient, deps.historyStore, deps.streamSrv.Favorites(), deps.downloadsStore))
		api.GET("/search/stream", handlers.SearchSSE(deps.jackettClient, deps.historyStore, deps.streamSrv.Favorites(), deps.downloadsStore))
		api.GET("/indexers", handlers.GetIndexers(deps.jackettClient))
		api.POST("/download", handlers.Download(deps.cfg))
		api.GET("/clients", handlers.GetClients(deps.cfg))
		api.GET("/proxy/torrent", handlers.ProxyTorrentDownload(deps.jackettClient))
		api.GET("/convert/torrent-to-magnet", handlers.ConvertTorrentToMagnet())
		api.GET("/convert/magnet-to-torrent", handlers.ConvertMagnetToTorrent(deps.streamSrv))

		adminAPI := api.Group("")
		if deps.tokenMgr != nil {
			adminAPI.Use(auth.AdminOnly())
		}
		adminAPI.GET("/config", handlers.GetConfig(deps.cfg, deps.configPath))
		adminAPI.PUT("/config", handlers.UpdateConfig(deps.cfg, deps.configPath, deps.jackettClient, deps.streamSrv))
		adminAPI.GET("/stream/settings", handlers.StreamGetSettings(deps.cfg, deps.streamSrv))
		adminAPI.PUT("/stream/settings", handlers.StreamUpdateSettings(deps.cfg, deps.configPath, deps.streamSrv))
		adminAPI.PUT("/downloads/settings", handlers.DownloadsUpdateSettings(deps.cfg, deps.configPath, func(n int) {
			if deps.streamSrv != nil {
				deps.streamSrv.SetVerifyConcurrency(n)
			}
		}))
		adminAPI.GET("/mounts", handlers.MountsGet(deps.cfg))
		adminAPI.PUT("/mounts", handlers.MountsUpdate(deps.cfg, deps.configPath, deps.localBrowser))
		adminAPI.POST("/config/test", handlers.TestJackett(deps.cfg))
		adminAPI.GET("/ai/benchmark", handlers.GetAIBenchmark(deps.aiClient, deps.aiBench, deps.aiBenchRun))
		adminAPI.POST("/ai/benchmark", handlers.RunAIBenchmark(deps.aiClient, deps.aiBench, deps.aiBenchRun))
		adminAPI.POST("/ai/benchmark/cancel", handlers.CancelAIBenchmark(deps.aiBenchRun))
		adminAPI.POST("/ai/benchmark/rerun-incomplete", handlers.RunAIBenchmarkIncomplete(deps.aiClient, deps.aiBench, deps.aiBenchRun))
		adminAPI.PUT("/ai/benchmark/cases", handlers.PutAICases(deps.aiBench))
		adminAPI.PUT("/ai/settings", handlers.PutAICostConfig(deps.aiClient, deps.aiBench))

		if deps.downloadsStore != nil && deps.authStore != nil {
			adminAPI.GET("/downloads/all", handlers.DownloadsListAll(deps.downloadsStore, deps.authStore, deps.streamSrv, deps.localBrowser))
			adminAPI.GET("/downloads/users", handlers.DownloadsUsers(deps.downloadsStore, deps.authStore))
		}

		api.GET("/status", handlers.Status(deps.jackettClient, deps.historyStore))

		registerHistoryRoutes(api, deps)
		registerStreamRoutes(api, adminAPI, deps)
		registerLibraryRoutes(api, deps)
		registerTMDBRoutes(api, deps)
		registerWatchlistRoutes(api, deps)
		registerPlaylistRoutes(api, deps)
		registerSubtitleRoutes(api, deps)
		registerTranscodeRoutes(api)
		registerAuthRoutes(api, deps)
		registerSidecarRoutes(api, deps)
	}
	return router
}

func emailStatus(mlr *mailer.Mailer) string {
	if mlr.Enabled() {
		return "enabled"
	}
	return "disabled — reset/verify/invite links are logged, not emailed"
}

func registerHistoryRoutes(api *gin.RouterGroup, deps *appDeps) {
	if deps.historyStore == nil {
		return
	}
	api.GET("/history", handlers.GetHistory(deps.historyStore))
	api.GET("/history/results", handlers.GetHistoryResults(deps.historyStore, deps.streamSrv.Favorites(), deps.downloadsStore))
	api.GET("/history/cache", handlers.SearchCache(deps.historyStore, deps.streamSrv.Favorites(), deps.downloadsStore))
	api.DELETE("/history", handlers.DeleteHistory(deps.historyStore))
	api.POST("/history/:id/refresh", handlers.HistoryRefresh(deps.historyStore, deps.jackettClient))
}

func registerStreamRoutes(api, adminAPI *gin.RouterGroup, deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	// Reads of the shared swarm stay on `api` (Downloads UI polls active/rate).
	// Global mutations that affect every user are admin-only.
	api.GET("/stream/cache", handlers.StreamCacheStats(deps.streamSrv))
	adminAPI.DELETE("/stream/cache", handlers.StreamCacheClear(deps.streamSrv))
	api.GET("/stream/rate", handlers.StreamRateStats(deps.streamSrv))
	api.GET("/stream/active", handlers.StreamActive(deps.streamSrv))
	adminAPI.POST("/stream/active/pause", handlers.StreamPauseAll(deps.streamSrv))
	adminAPI.POST("/stream/active/resume", handlers.StreamResumeAll(deps.streamSrv))
	api.GET("/stream/limits", handlers.StreamGetLimits(deps.streamSrv))
	adminAPI.POST("/stream/limits", handlers.StreamSetLimits(deps.streamSrv))
	api.POST("/stream/:hash/pause", handlers.StreamPause(deps.streamSrv))
	api.POST("/stream/:hash/resume", handlers.StreamResume(deps.streamSrv))
	api.POST("/stream/:hash/priority", handlers.StreamSetPriority(deps.streamSrv))
	api.POST("/stream/:hash/files/:idx/priority", handlers.StreamSetFilePriority(deps.streamSrv))
	api.GET("/stream/favorites", handlers.StreamFavorites(deps.streamSrv))
	api.POST("/stream/favorite", handlers.StreamFavorite(deps.streamSrv))
	api.DELETE("/stream/favorite/:name", handlers.StreamUnfavorite(deps.streamSrv))
	// Batch favorites (Perf #9): multi-select move/delete on FavoritesPage.
	api.POST("/stream/favorites/batch/remove", handlers.FavoritesBatchRemove(deps.streamSrv))
	api.POST("/stream/favorites/batch/folder", handlers.FavoritesBatchSetFolder(deps.streamSrv))
	api.GET("/stream/favorites/folders", handlers.FoldersList(deps.streamSrv))
	api.POST("/stream/favorites/folders", handlers.FolderCreate(deps.streamSrv))
	api.PATCH("/stream/favorites/folders/:id", handlers.FolderPatch(deps.streamSrv))
	api.DELETE("/stream/favorites/folders/:id", handlers.FolderDelete(deps.streamSrv))
	api.PATCH("/stream/favorite/:name/folder", handlers.FavoriteMoveToFolder(deps.streamSrv))
	api.POST("/stream/import", handlers.StreamImport(deps.streamSrv))
	api.POST("/stream/add", handlers.StreamAdd(deps.streamSrv, deps.libraryStore))
	api.POST("/stream/add-file", handlers.StreamAddTorrentFile(deps.streamSrv))
	api.GET("/stream/info/:hash", handlers.StreamInfo(deps.streamSrv))
	api.GET("/stream/probe/:hash/:file", handlers.StreamProbe(deps.streamSrv))
	api.GET("/stream/audio/meta/:hash/:file", handlers.StreamAudioMeta(deps.streamSrv))
	api.GET("/stream/subtrack/:hash/:file/:track", handlers.StreamSubtitleExtract(deps.streamSrv))
	api.GET("/stream/playlist/:hash/:file", handlers.StreamPlaylistM3U(deps.streamSrv))
	api.POST("/stream/prefetch/:hash/:file", handlers.StreamPrefetch(deps.streamSrv))
	api.GET("/stream/artwork/:hash/:file", handlers.StreamArtwork(deps.streamSrv))
	api.GET("/stream/metadata/:hash", handlers.StreamMetadata(deps.streamSrv))
	api.POST("/stream/metadata/batch", handlers.StreamMetadataBatch(deps.streamSrv))
	api.GET("/stream/health/:hash", handlers.StreamHealth(deps.streamSrv))
	api.POST("/stream/health/batch", handlers.StreamHealthBatch(deps.streamSrv))
	api.GET("/stream/trackers/:hash", handlers.StreamTrackers(deps.streamSrv))
	api.GET("/stream/thumb/:hash/:file", handlers.StreamThumbnail(deps.streamSrv))
	api.GET("/stream/art/:hash", handlers.StreamArt(deps.streamSrv))
	api.POST("/stream/art/resolve/batch", handlers.ResolveArtBatch(deps.streamSrv, deps.tmdbClient, deps.aiClient, deps.webSearch))
	api.POST("/stream/art/:hash/resolve", handlers.ResolveArt(deps.streamSrv, deps.tmdbClient, deps.aiClient, deps.webSearch))
	api.GET("/stream/:hash/:file", handlers.StreamFile(deps.streamSrv, deps.downloadsStore))
	// Batch drop BEFORE the singular DELETE so gin does not treat "drop" as a hash.
	api.POST("/stream/drop/batch", handlers.StreamDropBatch(deps.streamSrv, deps.hlsMgr))
	api.DELETE("/stream/:hash", handlers.StreamDrop(deps.streamSrv, deps.hlsMgr))
	api.POST("/stream/:hash/viewer", handlers.StreamViewerOpen(deps.streamSrv))
	api.DELETE("/stream/:hash/viewer", handlers.StreamViewerClose(deps.streamSrv, deps.hlsMgr))
	api.GET("/stream/transcode/:hash/:file", handlers.TranscodeStream(deps.streamSrv, deps.downloadsStore))

	api.GET("/transfers", handlers.TransfersList(deps.transferTracker))
	api.DELETE("/transfers/:id", handlers.TransfersCancel(deps.transferTracker))

	registerLocalRoutes(api, deps)
	registerDownloadsRoutes(api, deps)
	registerHLSRoutes(api, adminAPI, deps)
	registerPreviewRoutes(api, deps)
}

// registerPreviewRoutes wires the universal viewer endpoints (archives,
// comics, EPUB) — sources: torrent file (?hash=&idx=) or local mount
// (?mount=&path=). All GET, all under /api/preview/ (whitelisted in
// auth.isMediaPath for the ?token= fallback used by <img>/<iframe>).
func registerPreviewRoutes(api *gin.RouterGroup, deps *appDeps) {
	// LocalHiddenGate is a no-op when the request uses ?hash= (torrent source)
	// instead of ?mount=&path= — only local previews are curtain-gated.
	gate := lh.LocalHiddenGate(deps.streamSrv)
	d := handlers.PreviewDeps{Streamer: deps.streamSrv, Downloads: deps.downloadsStore, Local: deps.localBrowser}
	api.GET("/preview/archive", gate, handlers.PreviewArchiveList(d))
	api.GET("/preview/archive/entry", gate, handlers.PreviewArchiveEntry(d))
	api.GET("/preview/comic", gate, handlers.PreviewComicManifest(d))
	api.GET("/preview/comic/page", gate, handlers.PreviewComicPage(d))
	api.GET("/preview/epub", gate, handlers.PreviewEpubManifest(d))
	api.GET("/preview/epub/chapter", gate, handlers.PreviewEpubChapter(d))
	api.GET("/preview/epub/res", gate, handlers.PreviewEpubResource(d))
}

func registerLocalRoutes(api *gin.RouterGroup, deps *appDeps) {
	// Hard curtain: any query-based (mount,path) resolution must go through the
	// gate while the easter-egg reveal is closed. JSON-body mutations call
	// AbortIfLocalPathHidden after bind. POST /local/hidden is intentionally
	// ungated so the user can still hide/unhide when the curtain is open.
	gate := lh.LocalHiddenGate(deps.streamSrv)
	api.GET("/local/mounts", lh.LocalMounts(deps.localBrowser))
	api.GET("/local/list", gate, lh.LocalList(deps.localBrowser, deps.streamSrv))
	api.POST("/local/hidden", lh.LocalSetHidden(deps.localBrowser, deps.streamSrv))
	api.GET("/local/hidden", lh.LocalListHidden(deps.streamSrv))
	api.GET("/local/file", gate, lh.LocalFile(deps.localBrowser, deps.localStream, deps.localCache))
	api.GET("/local/transfer-status", gate, lh.LocalTransferStatus(deps.localBrowser, deps.localStream))
	api.POST("/local/cache", gate, lh.LocalCacheStart(deps.localBrowser, deps.localCache))
	api.POST("/local/cache/folder", gate, lh.LocalCacheFolder(deps.localBrowser, deps.localCache, deps.streamSrv))
	api.GET("/local/cache/status", gate, lh.LocalCacheStatus(deps.localBrowser, deps.localCache))
	api.DELETE("/local/cache", gate, lh.LocalCacheDelete(deps.localBrowser, deps.localCache))
	api.GET("/local/thumb", gate, lh.LocalThumb(deps.localBrowser))
	api.GET("/local/transcode", gate, lh.LocalTranscode(deps.localBrowser))
	api.DELETE("/local/file", gate, lh.LocalDelete(deps.localBrowser, deps.downloadsStore, deps.streamSrv))
	api.POST("/local/clean-empty", gate, lh.LocalCleanEmptyDirs(deps.localBrowser))
	api.GET("/local/duplicates", gate, lh.LocalDuplicates(deps.localBrowser, deps.streamSrv))
	api.POST("/local/duplicates/delete", lh.LocalDuplicatesDelete(deps.localBrowser, deps.downloadsStore, deps.streamSrv))
	api.POST("/local/promote", lh.LocalPromote(lh.LocalPromoteDeps{
		Browser:    deps.localBrowser,
		AIClient:   deps.aiClient,
		TMDBClient: deps.tmdbClient,
		SharedDir:  deps.cfg.Stream.SharedDir,
		Dests:      deps.promoteDests,
		Downloads:  deps.downloadsStore,
		Streamer:   deps.streamSrv,
		Tracker:    deps.transferTracker,
	}))
	api.POST("/local/promote/preview", lh.LocalPromotePreview(deps.localBrowser, deps.aiClient, deps.tmdbClient, deps.cfg.Stream.SharedDir, deps.promoteDests, deps.streamSrv))
	api.GET("/local/walk", gate, lh.LocalWalk(deps.localBrowser, deps.streamSrv))
	api.POST("/local/move", lh.LocalMoveEntry(deps.localBrowser, deps.downloadsStore, deps.streamSrv, deps.transferTracker))
	api.POST("/local/rename", lh.LocalRename(deps.localBrowser, deps.downloadsStore, deps.streamSrv))
	api.POST("/local/lock", lh.LocalSetFolderLock(deps.localBrowser, deps.streamSrv))
	api.POST("/local/upload", gate, lh.LocalUpload(deps.localBrowser, int64(deps.cfg.External.MaxUploadMB)<<20))
	api.GET("/local/play", gate, lh.LocalPlay(deps.localBrowser, deps.libraryStore))
	api.POST("/local/play/batch", lh.LocalPlayBatch(deps.localBrowser, deps.streamSrv))
	api.GET("/local/audio/meta", gate, lh.LocalAudioMeta(deps.localBrowser, deps.audioMetaStore))
	api.GET("/local/audio/cover", gate, lh.LocalAudioCover(deps.localBrowser, deps.audioMetaStore, deps.webSearch))
	api.GET("/lyrics", handlers.LyricsGet(deps.lyricsClient))
	api.GET("/music/trending", handlers.MusicTrending(deps.musicTrending))
	api.GET("/local/probe", gate, lh.LocalProbe(deps.localBrowser))
	api.GET("/local/sidecars", gate, lh.LocalSidecars(deps.localBrowser))
	api.GET("/local/sidecar", gate, lh.LocalSidecarRead(deps.localBrowser))
	api.GET("/local/subtrack", gate, lh.LocalSubtitleExtract(deps.localBrowser, deps.localCache))
	if deps.subtitleClient != nil {
		api.GET("/local/subtitles/auto", gate, lh.LocalSubtitlesAuto(deps.localBrowser, deps.subtitleClient))
	}
}

// downloadRemoverDep returns the worker as the handlers' downloadRemover, or a
// true-nil interface when no worker is running. Returning the typed pointer
// directly would wrap a nil *Worker in a non-nil interface (the classic Go
// typed-nil trap), defeating the handler's nil guard.
func downloadRemoverDep(deps *appDeps) handlers.DownloadRemover {
	if deps.downloadsWkr == nil {
		return nil
	}
	return deps.downloadsWkr
}

func registerDownloadsRoutes(api *gin.RouterGroup, deps *appDeps) {
	if deps.downloadsStore == nil {
		return
	}
	api.GET("/downloads", handlers.DownloadsList(deps.downloadsStore, deps.streamSrv, deps.localBrowser, deps.authStore, deps.cfg.Stream.DownloadDir))
	api.GET("/downloads/filtered", handlers.DownloadsListFiltered(deps.downloadsStore, deps.streamSrv, deps.localBrowser, deps.authStore))
	api.GET("/downloads/trackers", handlers.DownloadsTrackers(deps.downloadsStore))
	api.GET("/downloads/categories", handlers.DownloadsCategories(deps.downloadsStore))
	api.POST("/downloads", handlers.DownloadsCreate(deps.downloadsStore, deps.destinations))
	api.POST("/downloads/batch", handlers.DownloadsBatchCreate(deps.downloadsStore, deps.destinations))
	// Cross-torrent dedup (#23): check which of a torrent's files the user already
	// has (by content) and link to them instead of re-downloading.
	api.POST("/downloads/dedup-check", handlers.DedupCheck(deps.streamSrv, deps.downloadsStore, deps.localBrowser))
	api.POST("/downloads/link", handlers.DedupLink(deps.downloadsStore, deps.localBrowser))
	api.GET("/downloads/destinations", handlers.DownloadsDestinations(deps.destinations))
	api.GET("/downloads/dest/browse", handlers.DownloadsDestinationBrowse(deps.destinations))
	api.DELETE("/downloads/:id", handlers.DownloadsDelete(deps.downloadsStore, downloadRemoverDep(deps)))
	api.GET("/downloads/:id/details", handlers.DownloadsDetails(deps.downloadsStore, deps.streamSrv))
	api.GET("/downloads/:id/peers", handlers.DownloadsPeers(deps.downloadsStore, deps.streamSrv))
	api.GET("/downloads/:id/sources", handlers.DownloadsSources(deps.downloadsStore))
	api.POST("/downloads/:id/recheck", handlers.DownloadsRecheck(deps.downloadsStore, deps.streamSrv))
	api.PATCH("/downloads/:id/pause", handlers.DownloadsPause(deps.downloadsStore))
	api.PATCH("/downloads/:id/resume", handlers.DownloadsResume(deps.downloadsStore))
	api.PATCH("/downloads/:id/priority", handlers.DownloadsSetPriority(deps.downloadsStore))
	api.GET("/downloads/settings", handlers.DownloadsGetSettings(deps.cfg))
	api.PATCH("/downloads/pause-all", handlers.DownloadsPauseAll(deps.downloadsStore))
	api.PATCH("/downloads/resume-all", handlers.DownloadsResumeAll(deps.downloadsStore))
	api.PATCH("/downloads/batch/pause", handlers.DownloadsBatchPause(deps.downloadsStore))
	api.PATCH("/downloads/batch/resume", handlers.DownloadsBatchResume(deps.downloadsStore))
	api.POST("/downloads/batch/delete", handlers.DownloadsBatchDelete(deps.downloadsStore, downloadRemoverDep(deps)))
	api.POST("/downloads/batch/stop-seed", handlers.DownloadsBatchStopSeed(deps.downloadsStore, deps.streamSrv))
	promoteDeps := handlers.PromoteDeps{
		Store:      deps.downloadsStore,
		Streamer:   deps.streamSrv,
		AIClient:   deps.aiClient,
		TMDBClient: deps.tmdbClient,
		SharedDir:  deps.cfg.Stream.SharedDir,
		Dests:      deps.promoteDests,
		Tracker:    deps.transferTracker,
		Pending:    deps.pendingTransfers,
		Cfg:        deps.cfg,
	}
	api.POST("/downloads/:id/promote", handlers.DownloadsPromote(promoteDeps))
	api.POST("/downloads/promote", handlers.DownloadsPromoteBatch(promoteDeps))
	api.POST("/downloads/promote/preview", handlers.DownloadsPromotePreview(deps.downloadsStore, deps.aiClient, deps.tmdbClient, deps.cfg.Stream.SharedDir, deps.promoteDests))
	api.GET("/downloads/promote/browse", handlers.DownloadsPromoteBrowse(deps.cfg.Stream.SharedDir, deps.promoteDests))
	api.GET("/promote/destinations", handlers.DownloadsPromoteDests(deps.cfg.Stream.SharedDir, deps.promoteDests))
	api.POST("/downloads/:id/stop-seed", handlers.DownloadsStopSeed(deps.downloadsStore, deps.streamSrv))
}

func registerHLSRoutes(api, adminAPI *gin.RouterGroup, deps *appDeps) {
	if deps.hlsMgr == nil {
		return
	}
	api.GET("/stream/hls/:hash/:file/index.m3u8", handlers.StreamHLSMaster(deps.streamSrv, deps.hlsMgr, deps.downloadsStore, deps.cfg))
	// Variantes do ladder ABR (HLS master, Phase 2). `v` é segmento estático →
	// coexiste com o wildcard `:seg` no mesmo nível (o gin avalia estáticos antes
	// do wildcard); `:variant` fica sob o nó estático, sem colisão com `:seg`.
	api.GET("/stream/hls/:hash/:file/v/:variant/index.m3u8", handlers.StreamHLSVariant(deps.streamSrv, deps.hlsMgr, deps.downloadsStore))
	api.GET("/stream/hls/:hash/:file/v/:variant/:seg", handlers.StreamHLSSegment(deps.streamSrv, deps.hlsMgr, deps.downloadsStore))
	// Renditions de áudio alternativas (EXT-X-MEDIA TYPE=AUDIO, HLS Phase 2 M2b).
	// `a` é segmento estático (coexiste com `v` e o wildcard `:seg`); o segmento
	// reusa StreamHLSSegment (a chave -ao{track} vem de hlsSessionKeyFromReq).
	api.GET("/stream/hls/:hash/:file/a/:track/index.m3u8", handlers.StreamHLSAudio(deps.streamSrv, deps.hlsMgr, deps.downloadsStore))
	api.GET("/stream/hls/:hash/:file/a/:track/:seg", handlers.StreamHLSSegment(deps.streamSrv, deps.hlsMgr, deps.downloadsStore))
	// Renditions de legenda WebVTT (EXT-X-MEDIA TYPE=SUBTITLES). A mini-playlist
	// referencia o endpoint /stream/subtrack existente (ExtractSubtitle → VTT).
	api.GET("/stream/hls/:hash/:file/sub/:track/index.m3u8", handlers.StreamHLSSubtitle(deps.streamSrv, deps.hlsMgr, deps.downloadsStore))
	api.GET("/stream/hls/:hash/:file/:seg", handlers.StreamHLSSegment(deps.streamSrv, deps.hlsMgr, deps.downloadsStore))
	gate := lh.LocalHiddenGate(deps.streamSrv)
	api.GET("/local/hls/index.m3u8", gate, lh.LocalHLSMaster(deps.localBrowser, deps.hlsMgr, deps.localStream, deps.localCache))
	api.GET("/local/hls/seg", gate, lh.LocalHLSSegment(deps.localBrowser, deps.hlsMgr))
	adminAPI.GET("/transcode/active", handlers.TranscodeActive(deps.hlsMgr))
	adminAPI.DELETE("/transcode/active/:key", handlers.TranscodeKill(deps.hlsMgr))
}

func registerTMDBRoutes(api *gin.RouterGroup, deps *appDeps) {
	// TMDB enrichment — optional poster + overview per torrent title
	api.GET("/tmdb/match", handlers.TmdbMatch(deps.tmdbClient))
	api.POST("/tmdb/match/batch", handlers.TmdbMatchBatch(deps.tmdbClient))
	api.GET("/tmdb/trending", handlers.TmdbTrending(deps.tmdbClient))
	api.GET("/tmdb/genres", handlers.TmdbGenres(deps.tmdbClient))
	api.GET("/tmdb/videos", handlers.TmdbVideos(deps.tmdbClient))
}

func registerLibraryRoutes(api *gin.RouterGroup, deps *appDeps) {
	if deps.libraryStore == nil {
		return
	}
	api.GET("/library", handlers.LibraryList(deps.libraryStore, deps.streamSrv))
	// Personalized recommendations derived from the watched library (additive).
	// streamSrv lets the generator drop hidden-folder titles from the seed.
	api.GET("/recommendations", handlers.Recommendations(deps.libraryStore, deps.streamSrv, deps.tmdbClient))
	// Persist a per-user dismissal so an ignored recommendation never returns.
	api.POST("/recommendations/dismiss", handlers.DismissRecommendation(deps.libraryStore))
	// Personal usage statistics (live aggregation; nil stores contribute zeroes).
	api.GET("/stats", handlers.Stats(deps.libraryStore, deps.downloadsStore, deps.historyStore, deps.watchlistStore))
	api.GET(routeLibraryID, handlers.LibraryGet(deps.libraryStore))
	api.PATCH(routeLibraryID, handlers.LibraryUpdateResume(deps.libraryStore))
	api.DELETE(routeLibraryID, handlers.LibraryDelete(deps.libraryStore))
	api.DELETE("/library", handlers.LibraryDeleteAll(deps.libraryStore))
}

func registerWatchlistRoutes(api *gin.RouterGroup, deps *appDeps) {
	// Web Push + in-app notification feed. Handlers tolerate nil stores (503 /
	// empty feed), so these are registered unconditionally.
	api.GET("/push/vapid", handlers.PushVapidKey(deps.pushSender))
	api.POST("/push/subscribe", handlers.PushSubscribe(deps.pushStore))
	api.POST("/push/unsubscribe", handlers.PushUnsubscribe(deps.pushStore))
	api.GET("/notifications", handlers.NotificationsList(deps.pushStore))
	api.POST("/notifications/read", handlers.NotificationsMarkRead(deps.pushStore))

	if deps.watchlistStore == nil {
		return
	}
	api.GET("/watchlists", handlers.WatchlistList(deps.watchlistStore))
	api.POST("/watchlists", handlers.WatchlistCreate(deps.watchlistStore, deps.watchlistWkr))
	api.PUT("/watchlists/:id", handlers.WatchlistUpdate(deps.watchlistStore))
	api.DELETE("/watchlists/:id", handlers.WatchlistDelete(deps.watchlistStore))
	api.GET("/watchlists/:id/hits", handlers.WatchlistHits(deps.watchlistStore))
	// Free-text → schedule via the AI chain (nil aiClient → 503 inside).
	api.POST("/watchlists/schedule/parse", handlers.WatchlistScheduleParse(deps.aiClient))
}

func registerPlaylistRoutes(api *gin.RouterGroup, deps *appDeps) {
	if deps.playlistsStore == nil {
		return
	}
	api.GET("/playlists", handlers.PlaylistsList(deps.playlistsStore))
	api.POST("/playlists", handlers.PlaylistsCreate(deps.playlistsStore))
	api.GET(routePlaylistID, handlers.PlaylistsGet(deps.playlistsStore))
	api.PATCH(routePlaylistID, handlers.PlaylistsUpdate(deps.playlistsStore))
	api.DELETE(routePlaylistID, handlers.PlaylistsDelete(deps.playlistsStore))
	api.POST("/playlists/:id/items", handlers.PlaylistsAddItem(deps.playlistsStore))
	api.DELETE("/playlists/:id/items/:itemId", handlers.PlaylistsRemoveItem(deps.playlistsStore))
	api.PATCH("/playlists/:id/items/:itemId", handlers.PlaylistsReorderItem(deps.playlistsStore))
}

func registerSidecarRoutes(api *gin.RouterGroup, deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	api.GET("/stream/sidecars/:hash/:file", handlers.StreamSidecars(deps.streamSrv))
	api.GET("/stream/sidecar/:hash/:file", handlers.StreamSidecarRead(deps.streamSrv))
}

func registerSubtitleRoutes(api *gin.RouterGroup, deps *appDeps) {
	api.GET("/subtitles/search", handlers.SubtitlesSearch(deps.subtitleClient))
	api.GET("/subtitles/download/:fileId", handlers.SubtitlesDownload(deps.subtitleClient))
	api.GET("/subtitles/enabled", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"enabled": deps.subtitleClient.Enabled()})
	})
	if deps.streamSrv != nil {
		api.GET("/subtitles/auto/:hash/:file", handlers.SubtitlesAuto(deps.streamSrv, deps.subtitleClient))
	}
}

func registerTranscodeRoutes(api *gin.RouterGroup) {
	api.GET("/transcode/capabilities", handlers.TranscodeCapabilities)
}

func registerAuthRoutes(api *gin.RouterGroup, deps *appDeps) {
	if deps.authStore == nil {
		return
	}
	api.GET("/auth/me", handlers.Me(deps.authStore))
	api.POST("/auth/password", handlers.ChangePassword(deps.authStore))
	api.POST("/auth/email", handlers.ChangeEmail(deps.authStore, deps.mlr, deps.cfg.BaseURL))
	api.POST("/auth/mfa/enroll", handlers.MFAEnrollStart(deps.authStore))
	api.POST("/auth/mfa/verify", handlers.MFAEnrollVerify(deps.authStore))
	api.POST("/auth/mfa/disable", handlers.MFADisable(deps.authStore))
	api.GET("/auth/mfa/backup-codes", handlers.MFABackupCodesStatus(deps.authStore))
	api.POST("/auth/mfa/backup-codes/regenerate", handlers.MFABackupCodesRegenerate(deps.authStore))
	api.POST("/auth/media-token", handlers.MediaToken(deps.authStore, deps.tokenMgr))
	api.POST("/auth/sessions", handlers.ListSessions(deps.authStore))
	api.POST("/auth/sessions/revoke-others", handlers.RevokeOtherSessions(deps.authStore))
	api.DELETE("/auth/sessions/:id", handlers.RevokeSession(deps.authStore))
	api.GET("/auth/passkey", handlers.PasskeyList(deps.authStore))
	api.POST("/auth/passkey/register/begin", handlers.PasskeyRegisterBegin(deps.authStore, deps.waManager))
	api.POST("/auth/passkey/register/finish", handlers.PasskeyRegisterFinish(deps.authStore, deps.waManager))
	api.DELETE("/auth/passkey/:id", handlers.PasskeyDelete(deps.authStore))
	api.POST("/user/ntfy-topic", handlers.SetNtfyTopic(deps.authStore))
	api.POST("/user/notify-test", handlers.NotifyTest(deps.cfg, deps.authStore))
	// Incognito session management
	api.DELETE("/user/incognito", handlers.ClearIncognito(deps.historyStore, deps.libraryStore))
	api.POST("/user/incognito/heartbeat", handlers.IncognitoHeartbeat())

	adminGroup := api.Group("/auth/users")
	adminGroup.Use(auth.AdminOnly())
	adminGroup.GET("", handlers.ListUsers(deps.authStore))
	adminGroup.POST("", handlers.CreateUser(deps.authStore))
	adminGroup.DELETE("/:id", handlers.DeleteUser(deps.authStore))
	adminGroup.PATCH("/:id/status", handlers.SetUserStatus(deps.authStore))
	adminGroup.POST("/invite", handlers.Invite(deps.authStore, deps.mlr, deps.cfg.BaseURL))
	adminGroup.POST("/:id/reset-password", handlers.AdminResetPassword(deps.authStore, deps.mlr, deps.cfg.BaseURL))
	adminGroup.GET("/:id/sessions", handlers.AdminListUserSessions(deps.authStore))
	adminGroup.DELETE("/:id/sessions", handlers.AdminRevokeUserSessions(deps.authStore))
	adminGroup.DELETE("/:id/sessions/:sid", handlers.AdminRevokeUserSession(deps.authStore))
}
