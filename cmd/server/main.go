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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"crypto/rand"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/gluetun"
	"github.com/lgldsilva/jackui/internal/handlers"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/imagesearch"
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/localstream"
	"github.com/lgldsilva/jackui/internal/mailer"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/playlists"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
	"github.com/lgldsilva/jackui/internal/tmdb"
	"github.com/lgldsilva/jackui/internal/transcode"
	"github.com/lgldsilva/jackui/internal/transmissionrpc"
	"github.com/lgldsilva/jackui/internal/watchlist"
	"github.com/lgldsilva/jackui/ui"
)

const (
	routeLibraryID  = "/library/:id"
	routePlaylistID = "/playlists/:id"
)

// resolvePeerPort picks the inbound BitTorrent peer port. Behind a VPN the port
// must be the provider's forwarded port (dynamic), so when
// JACKUI_GLUETUN_CONTROL_URL is set we ask gluetun for it — it takes
// precedence. Otherwise a fixed JACKUI_PEER_PORT override; else 0 (the streamer
// falls back to its default 51469).
func resolvePeerPort() int {
	if ctrl := os.Getenv("JACKUI_GLUETUN_CONTROL_URL"); ctrl != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		if p, err := gluetun.ForwardedPort(ctx, ctrl); err == nil {
			log.Printf("peer port: using gluetun forwarded port %d", p)
			return p
		} else {
			log.Printf("peer port: gluetun forwarded port unavailable (%v) — falling back", err)
		}
	}
	if v := os.Getenv("JACKUI_PEER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			return p
		}
	}
	return 0
}

// watchForwardedPort restarts the process when gluetun's forwarded port changes.
// anacrolix binds the peer port at boot, so re-binding to a new forwarded port
// needs a fresh client — a clean exit lets `restart: unless-stopped` recreate us
// and repick the port. Port changes are rare (only on VPN reconnect), so the
// occasional restart is acceptable.
func watchForwardedPort(ctrl string, current int) {
	for {
		time.Sleep(2 * time.Minute)
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		p, err := gluetun.ForwardedPort(ctx, ctrl)
		cancel()
		if err == nil && p > 0 && p != current {
			log.Printf("forwarded port changed %d→%d — exiting to rebind", current, p)
			os.Exit(0)
		}
	}
}

type appDeps struct {
	cfg            *config.Config
	configPath     string
	jackettClient  *jackett.Client
	localBrowser   *local.Browser
	historyStore   *history.Store
	streamSrv      *streamer.Streamer
	streamCfg      streamer.Config
	stateDir       string
	libraryStore   *library.Store
	playlistsStore *playlists.Store
	downloadsStore *downloads.Store
	tmdbClient     *tmdb.Client
	aiClient       *ai.Client
	aiBench        *ai.BenchmarkStore
	webSearch      *imagesearch.Chain
	watchlistStore *watchlist.Store
	subtitleClient *subtitles.Client
	authStore      *auth.Store
	tokenMgr       *auth.TokenManager
	waManager      *auth.WAManager
	loginLockout   *auth.Lockout
	mlr            *mailer.Mailer
	promoteDests   []handlers.PromoteDest
	hlsMgr         *transcode.HLSSessionManager
	localStream    *localstream.Registry
	cleanup        []func()
}

func (d *appDeps) addCleanup(fn func()) {
	d.cleanup = append(d.cleanup, fn)
}

func (d *appDeps) runCleanup() {
	for i := len(d.cleanup) - 1; i >= 0; i-- {
		d.cleanup[i]()
	}
}

func main() {
	deps := &appDeps{}
	deps.cfg, deps.configPath = loadConfig()
	jackettClient := jackett.New(deps.cfg.Jackett.URL, deps.cfg.Jackett.APIKey)
	deps.jackettClient = jackettClient
	deps.localBrowser = local.NewBrowser(deps.cfg.External.Mounts)
	deps.localStream = localstream.NewRegistry(deps.cfg.External.LocalReadaheadMB)
	deps.addCleanup(deps.localStream.Close)
	deps.webSearch = imagesearch.Default()
	deps.mlr = mailer.New(deps.cfg.SMTP)

	initHistoryStore(deps)
	deps.streamCfg, deps.stateDir = prepareStreamConfig(deps.cfg)
	// Persist local-file thumbnails (and negative markers) under the stream
	// DataDir so they survive restarts instead of regenerating in /tmp.
	handlers.SetLocalThumbCacheDir(filepath.Join(deps.streamCfg.DataDir, ".thumbs", "local"))
	initStreamer(deps)
	initLibraryStore(deps)
	initPlaylistsStore(deps)
	initDownloadsStore(deps)
	initTMDBClient(deps)
	initAIClient(deps)
	initWatchlistStore(deps)
	deps.subtitleClient = initSubtitles(deps.cfg)
	initAuth(deps)
	migrateUserSubpathMounts(deps)
	deps.promoteDests = buildPromoteDests(deps.cfg)
	initHLSManager(deps)

	// Incognito reaper: delete stale incognito data after 1h of inactivity
	// (tab closed / crash). Both stores are guaranteed initialized by here.
	handlers.StartIncognitoReaper(deps.historyStore, deps.libraryStore)

	startTranscodeProbe()

	defer deps.runCleanup()

	gin.SetMode(gin.ReleaseMode)
	router := setupRouter(deps)

	distFS := mustGetDistFS()
	fileServer := http.FileServer(http.FS(distFS))
	router.NoRoute(spaFallback(distFS, fileServer))

	addr := fmt.Sprintf(":%d", deps.cfg.Port)
	log.Printf("JackUI starting on http://localhost%s", addr)

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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown error: %v", err)
	}
	log.Printf("HTTP server encerrado — rodando cleanups (anacrolix, stores, worker)...")
}

func loadConfig() (*config.Config, string) {
	configPath := "config.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	return cfg, configPath
}

func initHistoryStore(deps *appDeps) {
	dbPath := deps.cfg.DBPath
	if dbPath == "" {
		dbPath = "./jackui.db"
	}
	store, err := history.New(dbPath)
	if err != nil {
		log.Printf("Warning: failed to open history store at %s: %v — history disabled", dbPath, err)
		return
	}
	deps.historyStore = store
	log.Printf("History store: %s", dbPath)
	go func() {
		for {
			time.Sleep(24 * time.Hour)
			store.Cleanup(90 * 24 * time.Hour)
		}
	}()
}

// transmissionRPCEnabled diz se a camada de compat Transmission RPC deve ser
// exposta. Opt-in (default OFF) por ser uma superfície RPC sensível.
func transmissionRPCEnabled() bool {
	v := os.Getenv("JACKUI_TRANSMISSION_RPC_ENABLED")
	return v == "1" || v == "true"
}

func prepareStreamConfig(cfg *config.Config) (streamer.Config, string) {
	sc := streamer.Config{
		DataDir:       cfg.Stream.DataDir,
		IdleTimeout:   time.Duration(cfg.Stream.IdleMinutes) * time.Minute,
		MetadataWait:  time.Duration(cfg.Stream.MetadataSeconds) * time.Second,
		MaxCacheSize:  int64(cfg.Stream.MaxCacheGB) * 1024 * 1024 * 1024,
		JackettAPIKey: cfg.Jackett.APIKey,
		// Performance / hardware tuning (ver config.StreamConfig).
		MaxDownloadRate:    cfg.Stream.MaxDownloadRate,
		MaxUploadRate:      cfg.Stream.MaxUploadRate,
		Readahead:          int64(cfg.Stream.ReadaheadMB) << 20,
		StorageBackend:     cfg.Stream.StorageBackend,
		MaxConnsPerTorrent: cfg.Stream.MaxConnsPerTorrent,
		HalfOpenConns:      cfg.Stream.HalfOpenConns,
		PeersHighWater:     cfg.Stream.PeersHighWater,
		PieceHashers:       cfg.Stream.PieceHashers,
	}
	if u, perr := url.Parse(cfg.Jackett.URL); perr == nil {
		sc.JackettHost = u.Hostname()
	}
	// Inbound BitTorrent peer port. Behind a VPN it must be the provider's
	// forwarded port (read from gluetun) so peers can reach us — seeds public
	// torrents properly and improves leech. 0 → streamer default (51469).
	sc.ListenPort = resolvePeerPort()
	if ctrl := os.Getenv("JACKUI_GLUETUN_CONTROL_URL"); ctrl != "" && sc.ListenPort > 0 {
		go watchForwardedPort(ctrl, sc.ListenPort)
	}
	if sc.DataDir == "" {
		sc.DataDir = "/data/streams"
	}
	stateDir := cfg.Stream.StateDir
	if stateDir == "" {
		stateDir = sc.DataDir
	}
	return sc, stateDir
}

func initStreamer(deps *appDeps) {
	s, err := streamer.New(deps.streamCfg)
	if err != nil {
		log.Printf("Warning: streamer init failed: %v — streaming disabled", err)
		return
	}
	deps.streamSrv = s
	deps.addCleanup(func() { s.Close() })
	log.Printf("Streamer ready: %s (idle=%s, metadata=%s)", deps.streamCfg.DataDir, deps.streamCfg.IdleTimeout, deps.streamCfg.MetadataWait)

	if favs, ferr := streamer.NewFavorites(streamer.DefaultFavoritesPath(deps.stateDir)); ferr == nil {
		s.SetFavorites(favs)
		deps.addCleanup(func() { favs.Close() })
		log.Printf("Favorites: %s", streamer.DefaultFavoritesPath(deps.stateDir))
	} else {
		log.Printf("Warning: favorites store init failed: %v", ferr)
	}
	if mc, mcerr := streamer.NewMetadataCache(streamer.DefaultMetadataCachePath(deps.stateDir)); mcerr == nil {
		s.SetMetadataCache(mc)
		deps.addCleanup(func() { _ = mc.Close() })
		log.Printf("Metadata cache: %s", streamer.DefaultMetadataCachePath(deps.stateDir))
	} else {
		log.Printf("Warning: metadata cache init failed: %v", mcerr)
	}
}

func initLibraryStore(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	libPath := deps.stateDir + "/.library.db"
	l, err := library.New(libPath)
	if err != nil {
		log.Printf("Warning: library store init failed: %v", err)
		return
	}
	deps.libraryStore = l
	deps.addCleanup(func() { l.Close() })
	log.Printf("Library: %s", libPath)
	if mc := deps.streamSrv.MetadataCache(); mc != nil {
		n, mErr := l.RefreshStalePrimary(func(hash string) (int, bool) {
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
}

func initPlaylistsStore(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	plPath := deps.stateDir + "/.playlists.db"
	p, err := playlists.New(plPath)
	if err != nil {
		log.Printf("Warning: playlists store init failed: %v", err)
		return
	}
	deps.playlistsStore = p
	deps.addCleanup(func() { p.Close() })
	log.Printf("Playlists: %s", plPath)
}

func initDownloadsStore(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	dlPath := deps.stateDir + "/.downloads.db"
	d, err := downloads.New(dlPath)
	if err != nil {
		log.Printf("Warning: downloads store init failed: %v", err)
		return
	}
	deps.downloadsStore = d
	deps.addCleanup(func() { d.Close() })
	log.Printf("Downloads: %s", dlPath)

	deps.streamSrv.SetFilePathResolver(func(h metainfo.Hash, fileIdx int) (string, bool) {
		path, err := d.GetCompletedPath(h.HexString(), fileIdx)
		if err != nil || path == "" {
			return "", false
		}
		if _, err := os.Stat(path); err != nil {
			return "", false
		}
		return path, true
	})

	usernameResolver := func(userID int) string {
		if deps.authStore == nil {
			return ""
		}
		u, err := deps.authStore.GetUserByID(userID)
		if err != nil || u == nil {
			return ""
		}
		return u.Username
	}
	// Live queue settings: the worker reads this each tick, so PUT /downloads/settings
	// (which mutates deps.cfg.DownloadsQueue) takes effect without a restart.
	queueSettings := func() downloads.QueueSettings {
		q := deps.cfg.DownloadsQueue
		return downloads.QueueSettings{
			MaxActive:         q.MaxActive,
			PerUserMaxActive:  q.PerUserMaxActive,
			StallThresholdMin: q.StallThresholdMin,
			MaxStalls:         q.MaxStalls,
			AgingStepMin:      q.AgingStepMin,
			AgingCap:          q.AgingCap,
			RotationEnabled:   q.RotationEnabled,
		}
	}
	worker := downloads.NewWorker(downloads.WorkerConfig{
		Store:           d,
		Streamer:        deps.streamSrv,
		DataDir:         deps.streamCfg.DataDir,
		DownloadDir:     deps.cfg.Stream.DownloadDir,
		Interval:        2 * time.Second,
		NtfyBaseURL:     deps.cfg.Notifications.NtfyBaseURL,
		NtfyTopic:       deps.cfg.Notifications.NtfyDefaultTopic,
		NtfyToken:       deps.cfg.Notifications.NtfyToken,
		ResolveUsername: usernameResolver,
		Settings:        queueSettings,
		Jackett:         deps.jackettClient,
	})
	worker.Start()
	deps.addCleanup(worker.Stop)
	log.Printf("Downloads worker started (tick=2s, ntfy=%q)", deps.cfg.Notifications.NtfyDefaultTopic)
}

// migrateUserSubpathMounts relocates loose files at the root of any per-user
// (UserSubpath) mount into the owner's subdir, so turning a previously-shared
// mount into per-user doesn't hide existing files. Ownership comes from the
// downloads store (file_path → user_id → username); files with no download
// record (manual uploads, external sources) fall back to the admin's subdir.
// Idempotent and logged — runs every boot, a no-op once everything is scoped.
func migrateUserSubpathMounts(deps *appDeps) {
	if deps.localBrowser == nil || deps.downloadsStore == nil || deps.authStore == nil {
		return
	}
	users, err := deps.authStore.ListUsers()
	if err != nil {
		log.Printf("Warning: usersubpath migration skipped (list users: %v)", err)
		return
	}
	known := make(map[string]bool, len(users))
	for _, u := range users {
		known[u.Username] = true
	}
	fallback := deps.cfg.Auth.AdminUsername
	attribute := buildOwnerAttributor(deps)

	for _, m := range deps.cfg.External.Mounts {
		if !m.UserSubpath {
			continue
		}
		res, err := deps.localBrowser.MigrateToUserSubpath(m.Name, known, fallback, attribute)
		if err != nil {
			log.Printf("Warning: usersubpath migration for %q failed: %v", m.Name, err)
			continue
		}
		logMigrationResult(m.Name, res)
	}
}

// buildOwnerAttributor returns an attribute func mapping an absolute path to
// its owner's username via the downloads store (file_path → user_id → username).
func buildOwnerAttributor(deps *appDeps) func(abs string) (string, bool) {
	return func(abs string) (string, bool) {
		dls, err := deps.downloadsStore.FindByPathPrefix(abs)
		if err != nil || len(dls) == 0 {
			return "", false
		}
		u, err := deps.authStore.GetUserByID(dls[0].UserID)
		if err != nil || u == nil {
			return "", false
		}
		return u.Username, true
	}
}

// logMigrationResult logs a per-mount migration summary and each moved entry;
// a no-op when nothing was moved.
func logMigrationResult(mount string, res local.MigrationResult) {
	if len(res.Moved) == 0 {
		return
	}
	log.Printf("UserSubpath migration %q: moved %d entr(ies), %d already scoped", mount, len(res.Moved), res.Skipped)
	for _, e := range res.Moved {
		suffix := ""
		if e.Fallback {
			suffix = " (sem dono → admin)"
		}
		log.Printf("  • %s → %s/%s", e.Name, e.ToUser, e.Name+suffix)
	}
}

func initTMDBClient(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	tmdbPath := deps.stateDir + "/.tmdb-cache.db"
	tc, err := tmdb.New(deps.cfg.TMDB.APIKey, deps.cfg.TMDB.OMDbAPIKey, tmdbPath)
	if err != nil {
		log.Printf("Warning: tmdb client init failed: %v", err)
		return
	}
	deps.tmdbClient = tc
	deps.addCleanup(func() { _ = tc.Close() })
	if deps.cfg.TMDB.APIKey != "" {
		log.Printf("TMDB enrichment: enabled (cache: %s)", tmdbPath)
	} else {
		log.Printf("TMDB enrichment: disabled (no API key) — cache prepared at %s", tmdbPath)
	}
}

func initAIClient(deps *appDeps) {
	aiClient := ai.New(deps.cfg.AI)
	deps.aiClient = aiClient
	if aiClient == nil {
		log.Printf("AI title identification: disabled (no chain) — using regex title cleaning")
		return
	}
	bs, err := ai.NewBenchmarkStore(ai.DefaultBenchmarkStorePath(deps.stateDir))
	if err != nil {
		log.Printf("Warning: ai benchmark store init failed: %v", err)
	} else {
		deps.aiBench = bs
		deps.addCleanup(func() { _ = bs.Close() })
		if res := bs.Results(); len(res) > 0 {
			aiClient.AdoptBenchmark(res)
		}
		// A cost config set via the Settings UI overrides the env/yaml defaults.
		if cc, ok := bs.LoadCostConfig(); ok {
			aiClient.SetCostConfig(cc)
		}
	}
	ids := make([]string, 0, len(aiClient.Slots()))
	for _, s := range aiClient.Slots() {
		ids = append(ids, s.ID)
	}
	log.Printf("AI title identification: enabled — chain: %s", strings.Join(ids, " → "))
}

func initWatchlistStore(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	wlPath := deps.stateDir + "/.watchlist.db"
	w, err := watchlist.New(wlPath)
	if err != nil {
		log.Printf("Warning: watchlist store init failed: %v", err)
		return
	}
	deps.watchlistStore = w
	deps.addCleanup(func() { w.Close() })
	log.Printf("Watchlist: %s", wlPath)
	interval := time.Duration(deps.cfg.Notifications.WatchlistInterval) * time.Minute
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	notifier := &watchlist.NtfyPoster{BaseURL: deps.cfg.Notifications.NtfyBaseURL, Token: deps.cfg.Notifications.NtfyToken}
	worker := watchlist.NewWorker(w, deps.jackettClient, notifier, deps.cfg.Notifications.NtfyDefaultTopic, interval)
	worker.Start()
	deps.addCleanup(worker.Stop)
	log.Printf("Watchlist worker: interval=%s default_topic=%q", interval, deps.cfg.Notifications.NtfyDefaultTopic)
}

func initSubtitles(cfg *config.Config) *subtitles.Client {
	subCacheDir := cfg.Subtitles.CacheDir
	if subCacheDir == "" {
		subCacheDir = "/data/subtitles"
	}
	sc := subtitles.New(
		cfg.Subtitles.OpenSubtitlesAPIKey,
		cfg.Subtitles.OpenSubtitlesUsername,
		cfg.Subtitles.OpenSubtitlesPassword,
		subCacheDir,
	)
	if sc.Enabled() {
		log.Printf("Subtitles: OpenSubtitles enabled (cache=%s)", subCacheDir)
	}
	return sc
}

func startTranscodeProbe() {
	go func() {
		caps, err := transcode.Probe(context.Background(), false)
		if err != nil {
			log.Printf("Transcode probe failed: %v", err)
			return
		}
		log.Printf("Transcode: preferred H.264=%q, HEVC=%q (NVIDIA=%v VAAPI=%v QSV=%v)",
			caps.Preferred, caps.PreferredHE, caps.HasNVIDIA, caps.HasVAAPI, caps.HasQSV)
	}()
}

func initAuth(deps *appDeps) {
	deps.loginLockout = auth.NewLockout(5, 15*time.Minute)
	if !deps.cfg.Auth.Enabled {
		log.Printf("Auth disabled — all endpoints public (set JACKUI_AUTH_ENABLED=1 to enable)")
		return
	}
	initAuthStore(deps)
	initJWTSecret(deps)
	initPasskeys(deps)
	bootstrapAdmin(deps)
	go func() {
		for {
			time.Sleep(1 * time.Hour)
			deps.authStore.CleanupExpired()
		}
	}()
}

func initAuthStore(deps *appDeps) {
	authDB := deps.cfg.Auth.DBPath
	if authDB == "" {
		authDB = "/data/auth.db"
	}
	authStore, err := auth.New(authDB)
	if err != nil {
		log.Fatalf("Auth store init failed: %v", err)
	}
	deps.authStore = authStore
	deps.addCleanup(func() { authStore.Close() })
	log.Printf("Auth enabled: user store at %s", authDB)
}

func initJWTSecret(deps *appDeps) {
	secret := []byte(deps.cfg.Auth.JWTSecret)
	if len(secret) < 32 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			log.Fatalf("Failed to generate JWT secret: %v", err)
		}
		log.Printf("Auth: generated random JWT secret (set jwt_secret in config to persist across restarts)")
	}
	deps.tokenMgr = auth.NewTokenManager(secret, 15*time.Minute)
}

func initPasskeys(deps *appDeps) {
	if deps.cfg.BaseURL == "" {
		log.Printf("Passkeys (WebAuthn): disabled — set JACKUI_BASE_URL to the public https origin to enable")
		return
	}
	u, perr := url.Parse(deps.cfg.BaseURL)
	if perr != nil || u.Host == "" {
		log.Printf("Passkeys (WebAuthn): disabled — set JACKUI_BASE_URL to the public https origin to enable")
		return
	}
	origin := u.Scheme + "://" + u.Host
	wm, werr := auth.NewWAManager(u.Hostname(), "JackUI", origin)
	if werr != nil {
		log.Printf("Passkeys: disabled — %v", werr)
		return
	}
	deps.waManager = wm
	log.Printf("Passkeys (WebAuthn): enabled for %s (RPID=%s)", origin, u.Hostname())
}

func bootstrapAdmin(deps *appDeps) {
	adminUser := deps.cfg.Auth.AdminUsername
	if adminUser == "" {
		adminUser = "admin"
	}
	if deps.cfg.Auth.AdminPassword == "" {
		log.Fatalf("Auth enabled but JACKUI_ADMIN_PASSWORD / config admin_password not set")
	}
	if err := deps.authStore.Bootstrap(adminUser, deps.cfg.Auth.AdminPassword); err != nil {
		log.Fatalf("Auth bootstrap failed: %v", err)
	}
	log.Printf("Admin user=%s", adminUser)
}

func initHLSManager(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	hlsMgr, err := transcode.NewHLSManager(deps.streamCfg.DataDir)
	if err != nil {
		log.Printf("Warning: HLS manager init failed: %v — Safari users won't get HLS fallback", err)
		return
	}
	deps.hlsMgr = hlsMgr
}

func buildPromoteDests(cfg *config.Config) []handlers.PromoteDest {
	dests := make([]handlers.PromoteDest, 0, len(cfg.Stream.PromoteDirs))
	for _, pd := range cfg.Stream.PromoteDirs {
		dests = append(dests, handlers.PromoteDest{Name: pd.Name, Path: pd.Path})
	}
	return dests
}

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
			f.Close()
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}

func setupRouter(deps *appDeps) *gin.Engine {
	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization", "Range"}
	corsConfig.ExposeHeaders = []string{"Content-Length", "Content-Range", "Accept-Ranges"}
	router.Use(cors.New(corsConfig))

	router.GET("/healthz", handlers.Health(deps.historyStore))
	// Public build metadata (commit/build time/version) — checkable without a token.
	router.GET("/status", handlers.BuildInfo(deps.historyStore))
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
		)
		trpc.RegisterRoutes(router)
		log.Printf("Transmission RPC: /transmission/rpc (compat layer for *arr stack)")
	}

	log.Printf("Email (SMTP): %s", emailStatus(deps.mlr))

	if deps.authStore != nil && deps.tokenMgr != nil {
		pub := router.Group("/api/auth")
		pub.POST("/login", handlers.Login(deps.authStore, deps.tokenMgr, deps.loginLockout))
		pub.POST("/refresh", handlers.Refresh(deps.authStore, deps.tokenMgr))
		pub.POST("/logout", handlers.Logout(deps.authStore, deps.historyStore, deps.libraryStore))
		pub.POST("/register", handlers.Register(deps.authStore, deps.mlr, deps.cfg.BaseURL))
		pub.POST("/verify-email", handlers.VerifyEmail(deps.authStore))
		pub.POST("/forgot", handlers.Forgot(deps.authStore, deps.mlr, deps.cfg.BaseURL))
		pub.POST("/reset", handlers.Reset(deps.authStore))
		pub.POST("/passkey/login/begin", handlers.PasskeyLoginBegin(deps.authStore, deps.waManager))
		pub.POST("/passkey/login/finish", handlers.PasskeyLoginFinish(deps.authStore, deps.tokenMgr, deps.waManager))
	}

	api := router.Group("/api")
	if deps.tokenMgr != nil {
		api.Use(auth.Required(deps.tokenMgr))
		api.Use(auth.GuestRestrict())
	}
	api.Use(middleware.Incognito())
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
		adminAPI.PUT("/downloads/settings", handlers.DownloadsUpdateSettings(deps.cfg, deps.configPath))
		adminAPI.GET("/mounts", handlers.MountsGet(deps.cfg))
		adminAPI.PUT("/mounts", handlers.MountsUpdate(deps.cfg, deps.configPath, deps.localBrowser))
		adminAPI.POST("/config/test", handlers.TestJackett(deps.cfg))
		adminAPI.GET("/ai/benchmark", handlers.GetAIBenchmark(deps.aiClient, deps.aiBench))
		adminAPI.POST("/ai/benchmark", handlers.RunAIBenchmark(deps.aiClient, deps.aiBench))
		adminAPI.POST("/ai/benchmark/rerun-incomplete", handlers.RunAIBenchmarkIncomplete(deps.aiClient, deps.aiBench))
		adminAPI.PUT("/ai/benchmark/cases", handlers.PutAICases(deps.aiBench))
		adminAPI.PUT("/ai/settings", handlers.PutAICostConfig(deps.aiClient, deps.aiBench))

		if deps.downloadsStore != nil && deps.authStore != nil {
			adminAPI.GET("/downloads/all", handlers.DownloadsListAll(deps.downloadsStore, deps.authStore, deps.streamSrv))
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
	api.GET("/stream/cache", handlers.StreamCacheStats(deps.streamSrv))
	api.DELETE("/stream/cache", handlers.StreamCacheClear(deps.streamSrv))
	api.GET("/stream/rate", handlers.StreamRateStats(deps.streamSrv))
	api.GET("/stream/active", handlers.StreamActive(deps.streamSrv))
	api.POST("/stream/active/pause", handlers.StreamPauseAll(deps.streamSrv))
	api.POST("/stream/active/resume", handlers.StreamResumeAll(deps.streamSrv))
	api.GET("/stream/limits", handlers.StreamGetLimits(deps.streamSrv))
	api.POST("/stream/limits", handlers.StreamSetLimits(deps.streamSrv))
	api.POST("/stream/:hash/pause", handlers.StreamPause(deps.streamSrv))
	api.POST("/stream/:hash/resume", handlers.StreamResume(deps.streamSrv))
	api.POST("/stream/:hash/priority", handlers.StreamSetPriority(deps.streamSrv))
	api.POST("/stream/:hash/files/:idx/priority", handlers.StreamSetFilePriority(deps.streamSrv))
	api.GET("/stream/favorites", handlers.StreamFavorites(deps.streamSrv))
	api.POST("/stream/favorite", handlers.StreamFavorite(deps.streamSrv))
	api.DELETE("/stream/favorite/:name", handlers.StreamUnfavorite(deps.streamSrv))
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
	api.GET("/stream/subtrack/:hash/:file/:track", handlers.StreamSubtitleExtract(deps.streamSrv))
	api.GET("/stream/playlist/:hash/:file", handlers.StreamPlaylistM3U(deps.streamSrv))
	api.POST("/stream/prefetch/:hash/:file", handlers.StreamPrefetch(deps.streamSrv))
	api.GET("/stream/artwork/:hash/:file", handlers.StreamArtwork(deps.streamSrv))
	api.GET("/stream/metadata/:hash", handlers.StreamMetadata(deps.streamSrv))
	api.GET("/stream/health/:hash", handlers.StreamHealth(deps.streamSrv))
	api.GET("/stream/thumb/:hash/:file", handlers.StreamThumbnail(deps.streamSrv))
	api.GET("/stream/art/:hash", handlers.StreamArt(deps.streamSrv))
	api.POST("/stream/art/:hash/resolve", handlers.ResolveArt(deps.streamSrv, deps.tmdbClient, deps.aiClient, deps.webSearch))
	api.GET("/stream/:hash/:file", handlers.StreamFile(deps.streamSrv, deps.downloadsStore))
	api.DELETE("/stream/:hash", handlers.StreamDrop(deps.streamSrv, deps.hlsMgr))
	api.POST("/stream/:hash/viewer", handlers.StreamViewerOpen(deps.streamSrv))
	api.DELETE("/stream/:hash/viewer", handlers.StreamViewerClose(deps.streamSrv, deps.hlsMgr))
	api.GET("/stream/transcode/:hash/:file", handlers.TranscodeStream(deps.streamSrv, deps.downloadsStore))

	registerLocalRoutes(api, deps)
	registerDownloadsRoutes(api, deps)
	registerHLSRoutes(api, adminAPI, deps)
}

func registerLocalRoutes(api *gin.RouterGroup, deps *appDeps) {
	api.GET("/local/mounts", handlers.LocalMounts(deps.localBrowser))
	api.GET("/local/list", handlers.LocalList(deps.localBrowser))
	api.GET("/local/file", handlers.LocalFile(deps.localBrowser, deps.localStream))
	api.GET("/local/transfer-status", handlers.LocalTransferStatus(deps.localBrowser, deps.localStream))
	api.GET("/local/thumb", handlers.LocalThumb(deps.localBrowser))
	api.GET("/local/transcode", handlers.LocalTranscode(deps.localBrowser))
	api.DELETE("/local/file", handlers.LocalDelete(deps.localBrowser, deps.downloadsStore, deps.streamSrv))
	api.POST("/local/clean-empty", handlers.LocalCleanEmptyDirs(deps.localBrowser))
	api.POST("/local/promote", handlers.LocalPromote(deps.localBrowser, deps.aiClient, deps.tmdbClient, deps.cfg.Stream.SharedDir, deps.promoteDests, deps.downloadsStore, deps.streamSrv))
	api.POST("/local/promote/preview", handlers.LocalPromotePreview(deps.localBrowser, deps.aiClient, deps.tmdbClient, deps.cfg.Stream.SharedDir, deps.promoteDests))
	api.GET("/local/walk", handlers.LocalWalk(deps.localBrowser))
	api.POST("/local/move", handlers.LocalMoveEntry(deps.localBrowser, deps.downloadsStore, deps.streamSrv))
	api.POST("/local/upload", handlers.LocalUpload(deps.localBrowser, int64(deps.cfg.External.MaxUploadMB)<<20))
	api.GET("/local/play", handlers.LocalPlay(deps.localBrowser))
	api.GET("/local/probe", handlers.LocalProbe(deps.localBrowser))
	api.GET("/local/sidecars", handlers.LocalSidecars(deps.localBrowser))
	api.GET("/local/sidecar", handlers.LocalSidecarRead(deps.localBrowser))
	api.GET("/local/subtrack", handlers.LocalSubtitleExtract(deps.localBrowser))
	if deps.subtitleClient != nil {
		api.GET("/local/subtitles/auto", handlers.LocalSubtitlesAuto(deps.localBrowser, deps.subtitleClient))
	}
}

func registerDownloadsRoutes(api *gin.RouterGroup, deps *appDeps) {
	if deps.downloadsStore == nil {
		return
	}
	api.GET("/downloads", handlers.DownloadsList(deps.downloadsStore, deps.streamSrv, deps.cfg.Stream.DownloadDir))
	api.GET("/downloads/filtered", handlers.DownloadsListFiltered(deps.downloadsStore, deps.streamSrv))
	api.GET("/downloads/trackers", handlers.DownloadsTrackers(deps.downloadsStore))
	api.GET("/downloads/categories", handlers.DownloadsCategories(deps.downloadsStore))
	api.POST("/downloads", handlers.DownloadsCreate(deps.downloadsStore))
	api.DELETE("/downloads/:id", handlers.DownloadsDelete(deps.downloadsStore))
	api.GET("/downloads/:id/details", handlers.DownloadsDetails(deps.downloadsStore, deps.streamSrv))
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
	api.POST("/downloads/batch/delete", handlers.DownloadsBatchDelete(deps.downloadsStore))
	api.POST("/downloads/:id/promote", handlers.DownloadsPromote(deps.downloadsStore, deps.streamSrv, deps.aiClient, deps.tmdbClient, deps.cfg.Stream.SharedDir, deps.promoteDests))
	api.POST("/downloads/promote", handlers.DownloadsPromoteBatch(deps.downloadsStore, deps.streamSrv, deps.aiClient, deps.tmdbClient, deps.cfg.Stream.SharedDir, deps.promoteDests))
	api.POST("/downloads/promote/preview", handlers.DownloadsPromotePreview(deps.downloadsStore, deps.aiClient, deps.tmdbClient, deps.cfg.Stream.SharedDir, deps.promoteDests))
	api.GET("/downloads/promote/browse", handlers.DownloadsPromoteBrowse(deps.cfg.Stream.SharedDir, deps.promoteDests))
	api.GET("/promote/destinations", handlers.DownloadsPromoteDests(deps.cfg.Stream.SharedDir, deps.promoteDests))
	api.POST("/downloads/:id/stop-seed", handlers.DownloadsStopSeed(deps.downloadsStore, deps.streamSrv))
}

func registerHLSRoutes(api, adminAPI *gin.RouterGroup, deps *appDeps) {
	if deps.hlsMgr == nil {
		return
	}
	api.GET("/stream/hls/:hash/:file/index.m3u8", handlers.StreamHLSMaster(deps.streamSrv, deps.hlsMgr, deps.downloadsStore))
	api.GET("/stream/hls/:hash/:file/:seg", handlers.StreamHLSSegment(deps.streamSrv, deps.hlsMgr, deps.downloadsStore))
	api.GET("/local/hls/index.m3u8", handlers.LocalHLSMaster(deps.localBrowser, deps.hlsMgr, deps.localStream))
	api.GET("/local/hls/seg", handlers.LocalHLSSegment(deps.localBrowser, deps.hlsMgr))
	adminAPI.GET("/transcode/active", handlers.TranscodeActive(deps.hlsMgr))
	adminAPI.DELETE("/transcode/active/:key", handlers.TranscodeKill(deps.hlsMgr))
}

func registerTMDBRoutes(api *gin.RouterGroup, deps *appDeps) {
	// TMDB enrichment — optional poster + overview per torrent title
	api.GET("/tmdb/match", handlers.TmdbMatch(deps.tmdbClient))
	api.GET("/tmdb/trending", handlers.TmdbTrending(deps.tmdbClient))
	api.GET("/tmdb/genres", handlers.TmdbGenres(deps.tmdbClient))
}

func registerLibraryRoutes(api *gin.RouterGroup, deps *appDeps) {
	if deps.libraryStore == nil {
		return
	}
	api.GET("/library", handlers.LibraryList(deps.libraryStore))
	// Personalized recommendations derived from the watched library (additive).
	api.GET("/recommendations", handlers.Recommendations(deps.libraryStore, deps.tmdbClient))
	api.GET(routeLibraryID, handlers.LibraryGet(deps.libraryStore))
	api.PATCH(routeLibraryID, handlers.LibraryUpdateResume(deps.libraryStore))
	api.DELETE(routeLibraryID, handlers.LibraryDelete(deps.libraryStore))
	api.DELETE("/library", handlers.LibraryDeleteAll(deps.libraryStore))
}

func registerWatchlistRoutes(api *gin.RouterGroup, deps *appDeps) {
	if deps.watchlistStore == nil {
		return
	}
	api.GET("/watchlists", handlers.WatchlistList(deps.watchlistStore))
	api.POST("/watchlists", handlers.WatchlistCreate(deps.watchlistStore))
	api.PUT("/watchlists/:id", handlers.WatchlistUpdate(deps.watchlistStore))
	api.DELETE("/watchlists/:id", handlers.WatchlistDelete(deps.watchlistStore))
	api.GET("/watchlists/:id/hits", handlers.WatchlistHits(deps.watchlistStore))
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
}
