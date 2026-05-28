package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"crypto/rand"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/ai"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/config"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/handlers"
	"github.com/luizg/jackui/internal/history"
	"github.com/luizg/jackui/internal/imagesearch"
	"github.com/luizg/jackui/internal/jackett"
	"github.com/luizg/jackui/internal/library"
	"github.com/luizg/jackui/internal/local"
	"github.com/luizg/jackui/internal/mailer"
	"github.com/luizg/jackui/internal/middleware"
	"github.com/luizg/jackui/internal/playlists"
	"github.com/luizg/jackui/internal/streamer"
	"github.com/luizg/jackui/internal/subtitles"
	"github.com/luizg/jackui/internal/tmdb"
	"github.com/luizg/jackui/internal/transcode"
	"github.com/luizg/jackui/internal/watchlist"
	"github.com/luizg/jackui/ui"
)

func main() {
	configPath := "config.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	jackettClient := jackett.New(cfg.Jackett.URL, cfg.Jackett.APIKey)
	// Browser for filesystem mounts the user wants exposed (HD externo, NAS).
	// Browser is stateless; safe to construct unconditionally — empty mount
	// list means /api/local/* endpoints return 404 cleanly.
	localBrowser := local.NewBrowser(cfg.External.Mounts)

	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = "./jackui.db"
	}
	historyStore, err := history.New(dbPath)
	if err != nil {
		log.Printf("Warning: failed to open history store at %s: %v — history disabled", dbPath, err)
		historyStore = nil
	} else {
		log.Printf("History store: %s", dbPath)
		go func() {
			for {
				time.Sleep(24 * time.Hour)
				historyStore.Cleanup(90 * 24 * time.Hour)
			}
		}()
	}

	// Stream server (BitTorrent → HTTP video stream)
	streamCfg := streamer.Config{
		DataDir:       cfg.Stream.DataDir,
		IdleTimeout:   time.Duration(cfg.Stream.IdleMinutes) * time.Minute,
		MetadataWait:  time.Duration(cfg.Stream.MetadataSeconds) * time.Second,
		MaxCacheSize:  int64(cfg.Stream.MaxCacheGB) * 1024 * 1024 * 1024,
		JackettAPIKey: cfg.Jackett.APIKey,
	}
	// Trust the Jackett host in the SSRF guard + inject its apikey server-side.
	if u, perr := url.Parse(cfg.Jackett.URL); perr == nil {
		streamCfg.JackettHost = u.Hostname()
	}
	if streamCfg.DataDir == "" {
		streamCfg.DataDir = "/data/streams"
	}
	// stateDir holds the SQLite stores (favorites, library, etc.). When unset
	// (or in legacy deploys) it falls back to DataDir so behavior is unchanged;
	// pointing it at a faster/separate volume (e.g. /portainer state SSD) keeps
	// state writes out of the LVM piece cache that competes for I/O.
	stateDir := cfg.Stream.StateDir
	if stateDir == "" {
		stateDir = streamCfg.DataDir
	}
	streamSrv, err := streamer.New(streamCfg)
	if err != nil {
		log.Printf("Warning: streamer init failed: %v — streaming disabled", err)
		streamSrv = nil
	} else {
		log.Printf("Streamer ready: %s (idle=%s, metadata=%s)", streamCfg.DataDir, streamCfg.IdleTimeout, streamCfg.MetadataWait)
		defer streamSrv.Close()

		// Favorites store — preserved across cache evictions
		if favs, ferr := streamer.NewFavorites(streamer.DefaultFavoritesPath(stateDir)); ferr == nil {
			streamSrv.SetFavorites(favs)
			defer favs.Close()
			log.Printf("Favorites: %s", streamer.DefaultFavoritesPath(stateDir))
		} else {
			log.Printf("Warning: favorites store init failed: %v", ferr)
		}

		// Metadata cache — INDEPENDENT of favorites; persists TorrentInfo
		// snapshots so reopening a hash is instant. (Was nested inside the
		// favorites success branch, so a favorites-store failure silently took
		// the metadata cache + the RefreshStalePrimary migration down with it.)
		if mc, mcerr := streamer.NewMetadataCache(streamer.DefaultMetadataCachePath(stateDir)); mcerr == nil {
			streamSrv.SetMetadataCache(mc)
			defer mc.Close()
			log.Printf("Metadata cache: %s", streamer.DefaultMetadataCachePath(stateDir))
		} else {
			log.Printf("Warning: metadata cache init failed: %v", mcerr)
		}
	}

	// Library store — per-user history of streamed torrents (magnet + resume position)
	var libraryStore *library.Store
	if streamSrv != nil {
		libPath := stateDir + "/.library.db"
		if l, lerr := library.New(libPath); lerr == nil {
			libraryStore = l
			defer l.Close()
			log.Printf("Library: %s", libPath)

			// One-shot startup migration: refresh primary_file_index for legacy rows
			// where it's still 0 (column default). Pulls the correct value from the
			// metadata cache when available — fixes the Breaking Bad/featurette bug
			// for users who streamed before pickPrimaryFile learned to skip extras.
			if mc := streamSrv.MetadataCache(); mc != nil {
				n, mErr := libraryStore.RefreshStalePrimary(func(hash string) (int, bool) {
					meta := mc.Get(hash)
					if meta == nil {
						return 0, false
					}
					return meta.PrimaryFile, true
				})
				if mErr != nil {
					log.Printf("Library: stale primary refresh failed: %v", mErr)
				} else if n > 0 {
					log.Printf("Library: refreshed %d stale primary_file_index entries from metadata cache", n)
				}
			}
		} else {
			log.Printf("Warning: library store init failed: %v", lerr)
		}
	}

	// Playlists store — per-user ordered collections referencing torrents
	var playlistsStore *playlists.Store
	if streamSrv != nil {
		plPath := stateDir + "/.playlists.db"
		if p, perr := playlists.New(plPath); perr == nil {
			playlistsStore = p
			defer p.Close()
			log.Printf("Playlists: %s", plPath)
		} else {
			log.Printf("Warning: playlists store init failed: %v", perr)
		}
	}

	// Downloads store + worker — full-file background downloads. Worker
	// reconciles the DB queue with anacrolix every 2s; registered names are
	// protected from cache eviction.
	var downloadsStore *downloads.Store
	if streamSrv != nil {
		dlPath := stateDir + "/.downloads.db"
		if d, derr := downloads.New(dlPath); derr == nil {
			downloadsStore = d
			defer d.Close()
			log.Printf("Downloads: %s", dlPath)
			worker := downloads.NewWorker(downloadsStore, streamSrv, streamCfg.DataDir, cfg.Stream.DownloadDir, 2*time.Second)
			worker.Start()
			defer worker.Stop()
			log.Printf("Downloads worker started (tick=2s)")
		} else {
			log.Printf("Warning: downloads store init failed: %v", derr)
		}
	}

	// TMDB enrichment client — optional; nil-tolerant downstream.
	var tmdbClient *tmdb.Client
	if streamSrv != nil {
		tmdbPath := stateDir + "/.tmdb-cache.db"
		if tc, terr := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.OMDbAPIKey, tmdbPath); terr == nil {
			tmdbClient = tc
			defer tc.Close()
			if cfg.TMDB.APIKey != "" {
				log.Printf("TMDB enrichment: enabled (cache: %s)", tmdbPath)
			} else {
				log.Printf("TMDB enrichment: disabled (no API key) — cache prepared at %s", tmdbPath)
			}
		} else {
			log.Printf("Warning: tmdb client init failed: %v", terr)
		}
	}

	// AI title-identification chain — optional; nil falls back to TMDB's regex
	// title cleaning. Lights up automatically from GROQ_API_KEY/OPENROUTER_API_KEY/
	// OLLAMA_BASE_URL (see config.applyAIEnv).
	aiClient := ai.New(cfg.AI)
	var aiBench *ai.BenchmarkStore
	if aiClient != nil {
		if bs, berr := ai.NewBenchmarkStore(ai.DefaultBenchmarkStorePath(stateDir)); berr == nil {
			aiBench = bs
			defer bs.Close()
			// Adopt the last benchmark as the chain on boot (best model first +
			// discovered free local models as fallbacks), so a restart keeps the
			// tuned chain instead of falling back to the config defaults.
			if res := bs.Results(); len(res) > 0 {
				aiClient.AdoptBenchmark(res)
			}
		} else {
			log.Printf("Warning: ai benchmark store init failed: %v", berr)
		}
		ids := make([]string, 0, len(aiClient.Slots()))
		for _, s := range aiClient.Slots() {
			ids = append(ids, s.ID)
		}
		log.Printf("AI title identification: enabled — chain: %s", strings.Join(ids, " → "))
	} else {
		log.Printf("AI title identification: disabled (no chain) — using regex title cleaning")
	}

	// Web image search — keyless fallback for art when TMDB has no match (adult /
	// obscure titles). Only fires after a failed TMDB lookup. Routes through the
	// same egress as everything else (the VPN, in prod).
	webSearch := imagesearch.Default()

	// Watchlist store + background worker — polls Jackett periodically for saved
	// queries and pushes new matches to ntfy.sh. Worker starts only when both the
	// store and jackett client are healthy; partial init is OK.
	var watchlistStore *watchlist.Store
	if streamSrv != nil {
		wlPath := stateDir + "/.watchlist.db"
		if w, werr := watchlist.New(wlPath); werr == nil {
			watchlistStore = w
			defer w.Close()
			log.Printf("Watchlist: %s", wlPath)
			interval := time.Duration(cfg.Notifications.WatchlistInterval) * time.Minute
			if interval <= 0 {
				interval = 15 * time.Minute
			}
			notifier := &watchlist.NtfyPoster{BaseURL: cfg.Notifications.NtfyBaseURL}
			worker := watchlist.NewWorker(watchlistStore, jackettClient, notifier, cfg.Notifications.NtfyDefaultTopic, interval)
			worker.Start()
			defer worker.Stop()
			log.Printf("Watchlist worker: interval=%s default_topic=%q", interval, cfg.Notifications.NtfyDefaultTopic)
		} else {
			log.Printf("Warning: watchlist store init failed: %v", werr)
		}
	}

	// Subtitles (OpenSubtitles REST) — optional, only enabled if API key is set
	subCacheDir := cfg.Subtitles.CacheDir
	if subCacheDir == "" {
		subCacheDir = "/data/subtitles"
	}
	subtitleClient := subtitles.New(
		cfg.Subtitles.OpenSubtitlesAPIKey,
		cfg.Subtitles.OpenSubtitlesUsername,
		cfg.Subtitles.OpenSubtitlesPassword,
		subCacheDir,
	)
	if subtitleClient.Enabled() {
		log.Printf("Subtitles: OpenSubtitles enabled (cache=%s)", subCacheDir)
	}

	// Probe transcoding capabilities in background — non-blocking, takes ~10-30s on first run
	go func() {
		caps, err := transcode.Probe(context.Background(), false)
		if err != nil {
			log.Printf("Transcode probe failed: %v", err)
			return
		}
		log.Printf("Transcode: preferred H.264=%q, HEVC=%q (NVIDIA=%v VAAPI=%v QSV=%v)",
			caps.Preferred, caps.PreferredHE, caps.HasNVIDIA, caps.HasVAAPI, caps.HasQSV)
	}()

	// Auth — initialized only if enabled in config
	var authStore *auth.Store
	var tokenMgr *auth.TokenManager
	var waManager *auth.WAManager // passkeys; nil when BaseURL is unset
	// Brute-force guard: 5 consecutive failed logins lock the account for 15 min.
	loginLockout := auth.NewLockout(5, 15*time.Minute)
	if cfg.Auth.Enabled {
		authDB := cfg.Auth.DBPath
		if authDB == "" {
			authDB = "/data/auth.db"
		}
		var aerr error
		authStore, aerr = auth.New(authDB)
		if aerr != nil {
			log.Fatalf("Auth store init failed: %v", aerr)
		}
		defer authStore.Close()

		secret := []byte(cfg.Auth.JWTSecret)
		if len(secret) < 32 {
			// Auto-generate a strong secret if not provided
			secret = make([]byte, 32)
			if _, err := rand.Read(secret); err != nil {
				log.Fatalf("Failed to generate JWT secret: %v", err)
			}
			log.Printf("Auth: generated random JWT secret (set jwt_secret in config to persist across restarts)")
		}
		tokenMgr = auth.NewTokenManager(secret, 15*time.Minute)

		// Passkeys (WebAuthn) need the public origin to bind the RP ID. We derive
		// both from BaseURL (e.g. https://jackui.raspberrypi.lan → RPID host +
		// origin). Without it, passkey endpoints return 503 but the rest of auth
		// keeps working — TOTP/password are unaffected.
		if cfg.BaseURL != "" {
			if u, perr := url.Parse(cfg.BaseURL); perr == nil && u.Host != "" {
				origin := u.Scheme + "://" + u.Host
				wm, werr := auth.NewWAManager(u.Hostname(), "JackUI", origin)
				if werr != nil {
					log.Printf("Passkeys: disabled — %v", werr)
				} else {
					waManager = wm
					log.Printf("Passkeys (WebAuthn): enabled for %s (RPID=%s)", origin, u.Hostname())
				}
			}
		}
		if waManager == nil {
			log.Printf("Passkeys (WebAuthn): disabled — set JACKUI_BASE_URL to the public https origin to enable")
		}

		adminUser := cfg.Auth.AdminUsername
		if adminUser == "" {
			adminUser = "admin"
		}
		if cfg.Auth.AdminPassword == "" {
			log.Fatalf("Auth enabled but JACKUI_ADMIN_PASSWORD / config admin_password not set")
		}
		if err := authStore.Bootstrap(adminUser, cfg.Auth.AdminPassword); err != nil {
			log.Fatalf("Auth bootstrap failed: %v", err)
		}
		log.Printf("Auth enabled: user store at %s (admin user=%s)", authDB, adminUser)

		// Background cleanup of expired refresh tokens
		go func() {
			for {
				time.Sleep(1 * time.Hour)
				authStore.CleanupExpired()
			}
		}()
	} else {
		log.Printf("Auth disabled — all endpoints public (set JACKUI_AUTH_ENABLED=1 to enable)")
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	corsConfig := cors.DefaultConfig()
	// Self-hosted on LAN — open CORS so any device in the network can access.
	// Override via JACKUI_CORS_ALLOW_ORIGINS env if you want to restrict it.
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization", "Range"}
	corsConfig.ExposeHeaders = []string{"Content-Length", "Content-Range", "Accept-Ranges"}
	router.Use(cors.New(corsConfig))

	// Healthcheck — fast liveness probe, no external deps
	router.GET("/healthz", handlers.Health(historyStore))

	// Public auth config — frontend uses this to decide whether to show login screen
	router.GET("/api/auth/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"enabled": cfg.Auth.Enabled})
	})

	// Transactional email (reset / verify / invite). Disabled when SMTP is unset
	// — those flows then log the link instead of sending.
	mlr := mailer.New(cfg.SMTP)
	if mlr.Enabled() {
		log.Printf("Email (SMTP): enabled via %s:%d", cfg.SMTP.Host, cfg.SMTP.Port)
	} else {
		log.Printf("Email (SMTP): disabled — reset/verify/invite links are logged, not emailed")
	}

	// Public auth endpoints (no existing token needed).
	if authStore != nil && tokenMgr != nil {
		pub := router.Group("/api/auth")
		pub.POST("/login", handlers.Login(authStore, tokenMgr, loginLockout))
		pub.POST("/refresh", handlers.Refresh(authStore, tokenMgr))
		pub.POST("/logout", handlers.Logout(authStore))
		pub.POST("/register", handlers.Register(authStore, mlr, cfg.BaseURL))
		pub.POST("/verify-email", handlers.VerifyEmail(authStore))
		pub.POST("/forgot", handlers.Forgot(authStore, mlr, cfg.BaseURL))
		pub.POST("/reset", handlers.Reset(authStore))
		// Passkey login is public (it's an authentication method).
		pub.POST("/passkey/login/begin", handlers.PasskeyLoginBegin(authStore, waManager))
		pub.POST("/passkey/login/finish", handlers.PasskeyLoginFinish(authStore, tokenMgr, waManager))
	}

	api := router.Group("/api")
	// If auth enabled, all /api/* routes (except /api/auth/* already mounted above) require a valid token.
	if tokenMgr != nil {
		api.Use(auth.Required(tokenMgr))
	}
	// Incognito flag — tags the request when the client sent
	// X-JackUI-Incognito: 1 so history/library handlers can skip persistence.
	api.Use(middleware.Incognito())
	{
		// Client-side diagnostics — frontend posts here when something interesting
		// happens (codec failure, fallback fired, etc.) so we can grep server logs
		// instead of asking the user for browser console output.
		api.POST("/diag/log", handlers.ClientLog())

		api.GET("/search", handlers.Search(jackettClient, historyStore, streamSrv.Favorites(), downloadsStore))
		api.GET("/search/stream", handlers.SearchSSE(jackettClient, historyStore, streamSrv.Favorites(), downloadsStore))
		api.GET("/indexers", handlers.GetIndexers(jackettClient))
		api.POST("/download", handlers.Download(cfg))
		api.GET("/clients", handlers.GetClients(cfg))
		api.GET("/proxy/torrent", handlers.ProxyTorrentDownload(jackettClient))
		api.GET("/convert/torrent-to-magnet", handlers.ConvertTorrentToMagnet())
		api.GET("/convert/magnet-to-torrent", handlers.ConvertMagnetToTorrent(streamSrv))


		// Server config carries the Jackett API key and lets a caller rewrite
		// URLs/clients — admin-only. With auth OFF this degrades to public (same
		// as everything else); with auth ON, AdminOnly blocks non-admin users.
		adminAPI := api.Group("")
		if tokenMgr != nil {
			adminAPI.Use(auth.AdminOnly())
		}
		adminAPI.GET("/config", handlers.GetConfig(cfg, configPath))
		adminAPI.PUT("/config", handlers.UpdateConfig(cfg, configPath))
		adminAPI.POST("/config/test", handlers.TestJackett(cfg))

		// AI title-identification benchmark — admin-only (it spends external LLM
		// calls and re-orders the live chain).
		adminAPI.GET("/ai/benchmark", handlers.GetAIBenchmark(aiClient, aiBench))
		adminAPI.POST("/ai/benchmark", handlers.RunAIBenchmark(aiClient, aiBench))
		adminAPI.PUT("/ai/benchmark/cases", handlers.PutAICases(aiBench))

		api.GET("/status", handlers.Status(jackettClient, historyStore))

		if historyStore != nil {
			api.GET("/history", handlers.GetHistory(historyStore))
			api.GET("/history/results", handlers.GetHistoryResults(historyStore, streamSrv.Favorites(), downloadsStore))
			api.GET("/history/cache", handlers.SearchCache(historyStore, streamSrv.Favorites(), downloadsStore))
			api.DELETE("/history", handlers.DeleteHistory(historyStore))
			// Per-row swarm refresh — re-polls Jackett for current seeders/leechers.
			// 5min TTL cache per row keeps Jackett happy under spam-clicks.
			api.POST("/history/:id/refresh", handlers.HistoryRefresh(historyStore, jackettClient))
		}

		if streamSrv != nil {
			// Static paths first to win over /:hash wildcard
			api.GET("/stream/cache", handlers.StreamCacheStats(streamSrv))
			api.DELETE("/stream/cache", handlers.StreamCacheClear(streamSrv))
			api.GET("/stream/rate", handlers.StreamRateStats(streamSrv))
			// Transmission-style active-torrent controls — static paths win over /:hash
			api.GET("/stream/active", handlers.StreamActive(streamSrv))
			api.POST("/stream/active/pause", handlers.StreamPauseAll(streamSrv))
			api.POST("/stream/active/resume", handlers.StreamResumeAll(streamSrv))
			api.GET("/stream/limits", handlers.StreamGetLimits(streamSrv))
			api.POST("/stream/limits", handlers.StreamSetLimits(streamSrv))
			api.POST("/stream/:hash/pause", handlers.StreamPause(streamSrv))
			api.POST("/stream/:hash/resume", handlers.StreamResume(streamSrv))
			api.POST("/stream/:hash/priority", handlers.StreamSetPriority(streamSrv))
			api.POST("/stream/:hash/files/:idx/priority", handlers.StreamSetFilePriority(streamSrv))
			api.GET("/stream/favorites", handlers.StreamFavorites(streamSrv))
			api.POST("/stream/favorite", handlers.StreamFavorite(streamSrv))
			api.DELETE("/stream/favorite/:name", handlers.StreamUnfavorite(streamSrv))
			// Folder tree under favorites — lets users organize starred torrents
			// into categories/subcategories with drag-and-drop on the frontend.
			api.GET("/stream/favorites/folders", handlers.FoldersList(streamSrv))
			api.POST("/stream/favorites/folders", handlers.FolderCreate(streamSrv))
			api.PATCH("/stream/favorites/folders/:id", handlers.FolderPatch(streamSrv))
			api.DELETE("/stream/favorites/folders/:id", handlers.FolderDelete(streamSrv))
			api.PATCH("/stream/favorite/:name/folder", handlers.FavoriteMoveToFolder(streamSrv))
			// Import a torrent (magnet or .torrent file) straight into favorites,
			// bypassing search. Resolves hash+name locally; caches metainfo.
			api.POST("/stream/import", handlers.StreamImport(streamSrv))
			api.POST("/stream/add", handlers.StreamAdd(streamSrv, libraryStore))
			api.POST("/stream/add-file", handlers.StreamAddTorrentFile(streamSrv))
			api.GET("/stream/info/:hash", handlers.StreamInfo(streamSrv))
			api.GET("/stream/probe/:hash/:file", handlers.StreamProbe(streamSrv))
			// Subtrack and the raw stream file endpoint are hit by <video src>/<track src>
			// which can't send Authorization headers — they accept ?token= as fallback (handled by middleware).
			api.GET("/stream/subtrack/:hash/:file/:track", handlers.StreamSubtitleExtract(streamSrv))
			api.GET("/stream/playlist/:hash/:file", handlers.StreamPlaylistM3U(streamSrv))
			api.POST("/stream/prefetch/:hash/:file", handlers.StreamPrefetch(streamSrv))
			api.GET("/stream/artwork/:hash/:file", handlers.StreamArtwork(streamSrv))
			api.GET("/stream/metadata/:hash", handlers.StreamMetadata(streamSrv))
			api.GET("/stream/health/:hash", handlers.StreamHealth(streamSrv))
			api.GET("/stream/thumb/:hash/:file", handlers.StreamThumbnail(streamSrv))
			// Per-torrent resolved thumbnail (poster/cover/frame, persisted by info_hash).
			// GET is cheap (cards); POST runs the resolution chain on play.
			api.GET("/stream/art/:hash", handlers.StreamArt(streamSrv))
			api.POST("/stream/art/:hash/resolve", handlers.ResolveArt(streamSrv, tmdbClient, aiClient, webSearch))
			api.GET("/stream/:hash/:file", handlers.StreamFile(streamSrv))
			api.DELETE("/stream/:hash", handlers.StreamDrop(streamSrv))

			// Converte []config.PromoteDir → []handlers.PromoteDest (mesma
			// estrutura, pacotes diferentes).
			promoteDests := make([]handlers.PromoteDest, 0, len(cfg.Stream.PromoteDirs))
			for _, pd := range cfg.Stream.PromoteDirs {
				promoteDests = append(promoteDests, handlers.PromoteDest{Name: pd.Name, Path: pd.Path})
			}
			// External filesystem mounts — browse + stream files already on
			// disk (HD externo, NAS, OneDrive sync). Independent from torrents.
			api.GET("/local/mounts", handlers.LocalMounts(localBrowser))
			api.GET("/local/list", handlers.LocalList(localBrowser))
			api.GET("/local/file", handlers.LocalFile(localBrowser))
			api.GET("/local/thumb", handlers.LocalThumb(localBrowser))
			api.GET("/local/transcode", handlers.LocalTranscode(localBrowser))
			api.DELETE("/local/file", handlers.LocalDelete(localBrowser))
			api.POST("/local/promote", handlers.LocalPromote(localBrowser, cfg.Stream.SharedDir, promoteDests))
			// /local/play probes the file and tells the client whether to direct-play
			// or fetch the HLS master (for MKV / HEVC / AC3 / etc. that browsers
			// can't decode natively). Mirrors the torrent-side codec routing.
			api.GET("/local/play", handlers.LocalPlay(localBrowser))

			// Local subtitle pipeline — equivalentes às rotas /stream/{probe,
			// sidecars,sidecar,subtrack} + /subtitles/auto, mas keyed por
			// mount+path. Permite reusar o PlayerModal completo pra arquivos
			// locais (embedded, sidecar, OpenSubtitles, persistência por arquivo).
			api.GET("/local/probe", handlers.LocalProbe(localBrowser))
			api.GET("/local/sidecars", handlers.LocalSidecars(localBrowser))
			api.GET("/local/sidecar", handlers.LocalSidecarRead(localBrowser))
			api.GET("/local/subtrack", handlers.LocalSubtitleExtract(localBrowser))
			if subtitleClient != nil {
				api.GET("/local/subtitles/auto", handlers.LocalSubtitlesAuto(localBrowser, subtitleClient))
			}

			// Background full-file downloads (anacrolix Download API);
			// worker tick keeps the DB queue in sync with active torrents.
			if downloadsStore != nil {
				api.GET("/downloads", handlers.DownloadsList(downloadsStore))
				api.GET("/downloads/filtered", handlers.DownloadsListFiltered(downloadsStore))
				api.GET("/downloads/trackers", handlers.DownloadsTrackers(downloadsStore))
				api.GET("/downloads/categories", handlers.DownloadsCategories(downloadsStore))
				api.POST("/downloads", handlers.DownloadsCreate(downloadsStore))
				api.DELETE("/downloads/:id", handlers.DownloadsDelete(downloadsStore))
			api.GET("/downloads/:id/details", handlers.DownloadsDetails(downloadsStore, streamSrv))
			api.POST("/downloads/:id/recheck", handlers.DownloadsRecheck(downloadsStore, streamSrv))
				api.PATCH("/downloads/:id/pause", handlers.DownloadsPause(downloadsStore))
				api.PATCH("/downloads/:id/resume", handlers.DownloadsResume(downloadsStore))
				api.PATCH("/downloads/pause-all", handlers.DownloadsPauseAll(downloadsStore))
				api.PATCH("/downloads/resume-all", handlers.DownloadsResumeAll(downloadsStore))
				api.PATCH("/downloads/batch/pause", handlers.DownloadsBatchPause(downloadsStore))
				api.PATCH("/downloads/batch/resume", handlers.DownloadsBatchResume(downloadsStore))
				api.POST("/downloads/batch/delete", handlers.DownloadsBatchDelete(downloadsStore))
				// Promove um download concluído pro diretório compartilhado
				// (JACKUI_SHARED_DIR), opcionalmente em uma subpasta. Body:
				// { keepSeeding, targetSubdir, targetBase }.
				api.POST("/downloads/:id/promote", handlers.DownloadsPromote(downloadsStore, streamSrv, cfg.Stream.SharedDir, promoteDests))
				// Batch: promove N downloads pra mesma subpasta de destino.
				api.POST("/downloads/promote", handlers.DownloadsPromoteBatch(downloadsStore, streamSrv, cfg.Stream.SharedDir, promoteDests))
				// Lista subpastas do destino pra alimentar o navegador da UI.
				api.GET("/downloads/promote/browse", handlers.DownloadsPromoteBrowse(cfg.Stream.SharedDir, promoteDests))
				// Lista destinos de promoção disponíveis (GET /api/promote/destinations).
				api.GET("/promote/destinations", handlers.DownloadsPromoteDests(cfg.Stream.SharedDir, promoteDests))
				// Para de seedar sem mover o arquivo.
				api.POST("/downloads/:id/stop-seed", handlers.DownloadsStopSeed(downloadsStore, streamSrv))
			}

			// HLS — Safari-friendly playback path. Apple's MSE pipeline
			// rejects progressive fragmented MP4 over chunked transfer;
			// HLS (.m3u8 + .ts) is the only thing `<video src>` accepts
			// reliably across Safari/iOS. Frontend uses this URL as
			// fallback when Safari is detected or transcode-with-mp4 fails.
			hlsMgr, hlsErr := transcode.NewHLSManager(streamCfg.DataDir)
			if hlsErr != nil {
				log.Printf("Warning: HLS manager init failed: %v — Safari users won't get HLS fallback", hlsErr)
			} else {
				api.GET("/stream/hls/:hash/:file/index.m3u8", handlers.StreamHLSMaster(streamSrv, hlsMgr))
				api.GET("/stream/hls/:hash/:file/:seg", handlers.StreamHLSSegment(hlsMgr))
				// Same pipeline, different source: a local file on a configured
				// mount. Same reason for HLS as torrents — browsers can't decode
				// MKV / HEVC / AC3 in <video> directly; the transcode manager
				// dedupes by session key so concurrent viewers share one ffmpeg.
				api.GET("/local/hls/index.m3u8", handlers.LocalHLSMaster(localBrowser, hlsMgr))
				api.GET("/local/hls/seg", handlers.LocalHLSSegment(localBrowser, hlsMgr))
				log.Printf("HLS sessions: %s/hls", streamCfg.DataDir)
			}
		}

		// Library — per-user history of streamed torrents (magnet + resume)
		if libraryStore != nil {
			api.GET("/library", handlers.LibraryList(libraryStore))
			api.GET("/library/:id", handlers.LibraryGet(libraryStore))
			api.PATCH("/library/:id", handlers.LibraryUpdateResume(libraryStore))
			api.DELETE("/library/:id", handlers.LibraryDelete(libraryStore))
			api.DELETE("/library", handlers.LibraryDeleteAll(libraryStore))
		}

		// TMDB enrichment — optional poster + overview per torrent title
		api.GET("/tmdb/match", handlers.TmdbMatch(tmdbClient))
		api.GET("/tmdb/trending", handlers.TmdbTrending(tmdbClient))

		// Watchlists — saved searches + ntfy push notifications
		if watchlistStore != nil {
			api.GET("/watchlists", handlers.WatchlistList(watchlistStore))
			api.POST("/watchlists", handlers.WatchlistCreate(watchlistStore))
			api.PUT("/watchlists/:id", handlers.WatchlistUpdate(watchlistStore))
			api.DELETE("/watchlists/:id", handlers.WatchlistDelete(watchlistStore))
			api.GET("/watchlists/:id/hits", handlers.WatchlistHits(watchlistStore))
		}

		// Playlists — per-user ordered collections
		if playlistsStore != nil {
			api.GET("/playlists", handlers.PlaylistsList(playlistsStore))
			api.POST("/playlists", handlers.PlaylistsCreate(playlistsStore))
			api.GET("/playlists/:id", handlers.PlaylistsGet(playlistsStore))
			api.PATCH("/playlists/:id", handlers.PlaylistsUpdate(playlistsStore))
			api.DELETE("/playlists/:id", handlers.PlaylistsDelete(playlistsStore))
			api.POST("/playlists/:id/items", handlers.PlaylistsAddItem(playlistsStore))
			api.DELETE("/playlists/:id/items/:itemId", handlers.PlaylistsRemoveItem(playlistsStore))
			api.PATCH("/playlists/:id/items/:itemId", handlers.PlaylistsReorderItem(playlistsStore))
		}

		// Sidecar subtitles (.srt/.vtt files inside the torrent)
		if streamSrv != nil {
			api.GET("/stream/sidecars/:hash/:file", handlers.StreamSidecars(streamSrv))
			api.GET("/stream/sidecar/:hash/:file", handlers.StreamSidecarRead(streamSrv))
		}

		api.GET("/subtitles/search", handlers.SubtitlesSearch(subtitleClient))
		api.GET("/subtitles/download/:fileId", handlers.SubtitlesDownload(subtitleClient))
		api.GET("/subtitles/enabled", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"enabled": subtitleClient.Enabled()})
		})
		if streamSrv != nil {
			api.GET("/subtitles/auto/:hash/:file", handlers.SubtitlesAuto(streamSrv, subtitleClient))
		}

		// Hardware transcoding capability matrix
		api.GET("/transcode/capabilities", handlers.TranscodeCapabilities)

		// Authenticated user endpoints
		if authStore != nil {
			api.GET("/auth/me", handlers.Me(authStore))
			api.POST("/auth/password", handlers.ChangePassword(authStore))
			api.POST("/auth/mfa/enroll", handlers.MFAEnrollStart(authStore))
			api.POST("/auth/mfa/verify", handlers.MFAEnrollVerify(authStore))
			api.POST("/auth/mfa/disable", handlers.MFADisable(authStore))
			api.GET("/auth/mfa/backup-codes", handlers.MFABackupCodesStatus(authStore))
			api.POST("/auth/mfa/backup-codes/regenerate", handlers.MFABackupCodesRegenerate(authStore))
			// Media token: emite JWT scope="media" com TTL longo pra <video src>
			// não resetar quando o access token regular fizer refresh em background.
			api.POST("/auth/media-token", handlers.MediaToken(authStore, tokenMgr))
			// Active session management (list / revoke).
			api.POST("/auth/sessions", handlers.ListSessions(authStore))
			api.POST("/auth/sessions/revoke-others", handlers.RevokeOtherSessions(authStore))
			api.DELETE("/auth/sessions/:id", handlers.RevokeSession(authStore))
			// Passkey enrollment + management (authenticated).
			api.GET("/auth/passkey", handlers.PasskeyList(authStore))
			api.POST("/auth/passkey/register/begin", handlers.PasskeyRegisterBegin(authStore, waManager))
			api.POST("/auth/passkey/register/finish", handlers.PasskeyRegisterFinish(authStore, waManager))
			api.DELETE("/auth/passkey/:id", handlers.PasskeyDelete(authStore))
			adminGroup := api.Group("/auth/users")
			adminGroup.Use(auth.AdminOnly())
			adminGroup.GET("", handlers.ListUsers(authStore))
			adminGroup.POST("", handlers.CreateUser(authStore))
			adminGroup.DELETE("/:id", handlers.DeleteUser(authStore))
			adminGroup.PATCH("/:id/status", handlers.SetUserStatus(authStore))
			adminGroup.POST("/invite", handlers.Invite(authStore, mlr, cfg.BaseURL))
		}
		if streamSrv != nil {
			// Live transcode: remux/transcode torrent file → browser-friendly stream
			api.GET("/stream/transcode/:hash/:file", handlers.TranscodeStream(streamSrv))
		}
	}

	distFS, err := fs.Sub(ui.FS, "dist")
	if err != nil {
		log.Fatalf("Failed to create sub filesystem: %v", err)
	}

	fileServer := http.FileServer(http.FS(distFS))

	router.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		if strings.HasPrefix(path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "endpoint not found"})
			return
		}

		// Hashed assets (index-Bm63i5PK.js etc.) are content-addressed → safe to cache forever
		// HTML (index.html / unknown paths) must NOT be cached — Vite changes the hash inside on rebuild
		isHashedAsset := strings.HasPrefix(path, "/assets/") ||
			strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") ||
			strings.HasSuffix(path, ".woff2") || strings.HasSuffix(path, ".woff")

		if isHashedAsset {
			c.Writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// SPA shell + manifest + favicons — always revalidate so iOS PWA picks up new builds
			c.Writer.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			c.Writer.Header().Set("Pragma", "no-cache")
			c.Writer.Header().Set("Expires", "0")
		}

		f, err := distFS.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("JackUI starting on http://localhost%s", addr)

	// Graceful shutdown: SEM isto, o SIGTERM do Docker matava o processo
	// imediatamente e os `defer ...Close()` NUNCA rodavam — especialmente
	// `streamSrv.Close()` que comita o estado dos pieces do anacrolix no
	// `.torrent.bolt.db`. Resultado: bytes ficavam no disco mas o bolt DB
	// não sabia, e a cada restart o anacrolix re-baixava pieces que já estavam
	// completos. Esse é o motivo real do "download recomeçou do início" que
	// o usuário relatou ao longo do dia.
	srv := &http.Server{Addr: addr, Handler: router}
	serverErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		log.Fatalf("HTTP server failed: %v", err)
	case sig := <-quit:
		log.Printf("Signal %s recebido — graceful shutdown iniciado...", sig)
	}

	// Para o HTTP server primeiro (para de aceitar requests novos + drena os
	// em curso) e DEPOIS deixa os defers rodarem. Timeout de 25s cabe dentro
	// dos 30s default de stop-timeout do Docker.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown error: %v", err)
	}
	log.Printf("HTTP server encerrado — rodando cleanups (anacrolix, stores, worker)...")
	// O return de main() agora dispara todos os `defer` (streamSrv.Close,
	// libraryStore.Close, worker.Stop, ...) que antes eram puladados pelo
	// SIGKILL após o grace period de 10s.
}
