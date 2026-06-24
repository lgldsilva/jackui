package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/audiometa"
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
	"github.com/lgldsilva/jackui/internal/localcache"
	"github.com/lgldsilva/jackui/internal/localstream"
	"github.com/lgldsilva/jackui/internal/lyrics"
	"github.com/lgldsilva/jackui/internal/mailer"
	"github.com/lgldsilva/jackui/internal/metrics"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/musictrending"
	"github.com/lgldsilva/jackui/internal/playlists"
	"github.com/lgldsilva/jackui/internal/push"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
	"github.com/lgldsilva/jackui/internal/tmdb"
	"github.com/lgldsilva/jackui/internal/transcode"
	"github.com/lgldsilva/jackui/internal/transfer"
	"github.com/lgldsilva/jackui/internal/transmissionrpc"
	"github.com/lgldsilva/jackui/internal/watchlist"
	"github.com/lgldsilva/jackui/ui"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
		// gluetun's control server — and the VPN's forwarded port — can take tens of
		// seconds to come up after boot. A single query that loses this race binds
		// the WRONG port (inbound/seeding then never works) AND, because the watcher
		// below only starts when ListenPort>0 used to be the gate, could leave us
		// stuck on the fallback. So retry within a bounded window before giving up.
		deadline := time.Now().Add(peerPortBootTimeout)
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			p, err := gluetun.ForwardedPort(ctx, ctrl)
			cancel()
			if err == nil && p > 0 {
				log.Printf("peer port: using gluetun forwarded port %d", p)
				return p
			}
			if time.Now().After(deadline) {
				log.Printf("peer port: gluetun forwarded port unavailable after retries (%v) — falling back; watcher will rebind once gluetun is ready", err)
				break
			}
			time.Sleep(3 * time.Second)
		}
	}
	if v := os.Getenv("JACKUI_PEER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			return p
		}
	}
	return 0
}

// peerPortBootTimeout bounds how long resolvePeerPort waits for gluetun at boot.
// Generous enough to cover VPN connect + NAT-PMP acquisition; the watcher is the
// backstop if it's still not ready.
const peerPortBootTimeout = 2 * time.Minute

// watchForwardedPort restarts the process when gluetun's forwarded port changes.
// anacrolix binds the peer port at boot, so re-binding to a new forwarded port
// needs a fresh client — it signals `restart` so main runs the graceful
// shutdown and exits; `restart: unless-stopped` then recreates us and repicks
// the port. Port changes are rare (only on VPN reconnect), so the occasional
// restart is acceptable.
func watchForwardedPort(ctrl string, current int, restart chan<- struct{}) {
	for {
		time.Sleep(2 * time.Minute)
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		p, err := gluetun.ForwardedPort(ctx, ctrl)
		cancel()
		if err == nil && p > 0 && p != current {
			log.Printf("forwarded port changed %d→%d — triggering graceful restart to rebind", current, p)
			// Non-blocking: main drains this and runs graceful shutdown (closing
			// stores, stopping ffmpeg, waiting on moves). The old os.Exit(0) here
			// skipped all of that — SQLite mid-write, ffmpeg orphaned, downloads
			// stuck in `moving`. The watcher's job is done after one signal.
			select {
			case restart <- struct{}{}:
			default:
			}
			return
		}
	}
}

type appDeps struct {
	cfg             *config.Config
	configPath      string
	jackettClient   *jackett.Client
	localBrowser    *local.Browser
	historyStore    *history.Store
	streamSrv       *streamer.Streamer
	streamCfg       streamer.Config
	stateDir        string
	libraryStore    *library.Store
	audioMetaStore  *audiometa.Store
	lyricsClient    *lyrics.Client
	musicTrending   *musictrending.Client
	playlistsStore  *playlists.Store
	downloadsStore  *downloads.Store
	downloadsWkr    *downloads.Worker
	tmdbClient      *tmdb.Client
	aiClient        *ai.Client
	aiBench         *ai.BenchmarkStore
	webSearch       *imagesearch.Chain
	watchlistStore  *watchlist.Store
	watchlistWkr    *watchlist.Worker
	pushStore       *push.Store
	pushSender      *push.Sender
	subtitleClient  *subtitles.Client
	authStore       *auth.Store
	tokenMgr        *auth.TokenManager
	waManager       *auth.WAManager
	loginLockout    *auth.Lockout
	mlr             *mailer.Mailer
	promoteDests    []handlers.PromoteDest
	hlsMgr          *transcode.HLSSessionManager
	localStream     *localstream.Registry
	localCache      *localcache.Cache
	transferTracker *transfer.Tracker
	// restart is signalled by the gluetun forwarded-port watcher when the VPN
	// port changes. main's select drains it and runs the SAME graceful shutdown
	// as a SIGTERM (instead of os.Exit, which skipped every cleanup), then the
	// process exits and `restart: unless-stopped` recreates us to rebind.
	restart chan struct{}
	cleanup []func()
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
	setupLogger()
	deps := &appDeps{}
	deps.cfg, deps.configPath = loadConfig()
	if err := config.CheckWritable(deps.configPath); err != nil {
		log.Printf("WARNING: config %s não é gravável (%v) — alterações em Settings/Mounts não vão persistir; ajuste dono/permissão no host para o uid do container", deps.configPath, err)
	}
	jackettClient := jackett.New(deps.cfg.Jackett.URL, deps.cfg.Jackett.APIKey)
	deps.jackettClient = jackettClient
	deps.localBrowser = local.NewBrowser(deps.cfg.External.Mounts)
	deps.localStream = localstream.NewRegistry(deps.cfg.External.LocalReadaheadMB)
	deps.addCleanup(deps.localStream.Close)
	// Global move/copy progress tracker, shared by the post-download move (worker)
	// and the Local-tab/promote/AI moves (handlers) → the Transfers dock. The
	// concurrency cap (default 3; 0 → default) bounds simultaneous transfers; the
	// rest queue FIFO.
	deps.transferTracker = transfer.New(deps.cfg.Stream.MaxConcurrentTransfers)
	deps.webSearch = imagesearch.Default()
	deps.mlr = mailer.New(deps.cfg.SMTP)

	deps.restart = make(chan struct{}, 1)
	initHistoryStore(deps)
	deps.streamCfg, deps.stateDir = prepareStreamConfig(deps.cfg, deps.restart)
	// Persist local-file thumbnails (and negative markers) under the stream
	// DataDir so they survive restarts instead of regenerating in /tmp.
	handlers.SetLocalThumbCacheDir(filepath.Join(deps.streamCfg.DataDir, ".thumbs", "local"))
	// Dedicated cache for pre-fetching whole files from slow mounts (rclone) to
	// local disk — instant, seekable, EIO-proof playback. LRU-capped.
	if cache, cerr := localcache.New(filepath.Join(deps.streamCfg.DataDir, "local-cache"), deps.cfg.External.LocalCacheGB); cerr == nil {
		deps.localCache = cache
		deps.addCleanup(cache.Close)
	} else {
		log.Printf("Warning: local cache init failed: %v — local caching disabled", cerr)
	}
	initStreamer(deps)
	initLibraryStore(deps)
	initAudioMetaStore(deps)
	deps.lyricsClient = lyrics.New()         // public LrcLib proxy; no config/DB needed
	deps.musicTrending = musictrending.New() // keyless Apple RSS proxy; in-memory cache
	initPlaylistsStore(deps)
	initDownloadsStore(deps)
	initTMDBClient(deps)
	initAIClient(deps)
	initPushStore(deps)
	initWatchlistStore(deps)
	deps.subtitleClient = initSubtitles(deps.cfg)
	initAuth(deps)
	migrateUserSubpathMounts(deps)
	deps.promoteDests = buildPromoteDests(deps.cfg)
	initHLSManager(deps)

	// Incognito reaper: delete stale incognito data after 1h of inactivity
	// (tab closed / crash). Both stores are guaranteed initialized by here.
	handlers.StartIncognitoReaper(deps.historyStore, deps.libraryStore)

	if deps.streamSrv != nil {
		// Cancellable so graceful shutdown stops these background loops instead of
		// leaving them ticking against half-closed stores.
		workerCtx, cancelWorkers := context.WithCancel(context.Background())
		deps.addCleanup(cancelWorkers)
		metrics.StartWorker(workerCtx, deps.streamSrv, deps.hlsMgr)
		streamer.StartBandwidthScheduler(workerCtx, deps.streamSrv, deps.cfg)
	}

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
	case <-deps.restart:
		log.Printf("VPN forwarded port mudou — graceful shutdown para rebind (restart policy recria o processo)...")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown error: %v", err)
	}
	// HTTP is down → no new transfers can be submitted via the API. Give in-flight
	// moves a bounded window to finish before stores close; whatever doesn't drain
	// is picked up by downloads.RescueStuckMoving at next boot.
	if n := deps.transferTracker.ActiveCount(); n > 0 {
		log.Printf("Aguardando %d transferência(s) em andamento (até 20s)...", n)
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 20*time.Second)
		if deps.transferTracker.WaitIdle(waitCtx) {
			log.Printf("Transferências concluídas.")
		} else {
			log.Printf("Timeout — %d transferência(s) ainda ativa(s); serão retomadas no próximo boot.", deps.transferTracker.ActiveCount())
		}
		waitCancel()
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

func prepareStreamConfig(cfg *config.Config, restart chan<- struct{}) (streamer.Config, string) {
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
		SeedTrackers:       cfg.Stream.SeedTrackers,
	}
	if u, perr := url.Parse(cfg.Jackett.URL); perr == nil {
		sc.JackettHost = u.Hostname()
	}
	// Inbound BitTorrent peer port. Behind a VPN it must be the provider's
	// forwarded port (read from gluetun) so peers can reach us — seeds public
	// torrents properly and improves leech. 0 → streamer default (51469).
	sc.ListenPort = resolvePeerPort()
	if ctrl := os.Getenv("JACKUI_GLUETUN_CONTROL_URL"); ctrl != "" {
		// Always watch when behind gluetun — even if the boot resolve fell back. We
		// pass the EFFECTIVE port (the streamer's default when 0) so the watcher can
		// spot the real forwarded port once gluetun is ready and trigger a rebind.
		// Gating on ListenPort>0 used to leave a boot-race stuck on the fallback.
		effective := sc.ListenPort
		if effective == 0 {
			effective = streamer.DefaultPeerPort
		}
		go watchForwardedPort(ctrl, effective, restart)
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
	if seeds, serr := streamer.NewSeeds(streamer.DefaultSeedsPath(deps.stateDir)); serr == nil {
		s.SetSeeds(seeds)
		deps.addCleanup(func() { _ = seeds.Close() })
		log.Printf("Seeds store: %s", streamer.DefaultSeedsPath(deps.stateDir))
		go resumeSeeding(s, seeds)
	} else {
		log.Printf("Warning: seeds store init failed: %v", serr)
	}
}

// resumeSeeding re-adds every persisted seed-tracker torrent on boot so seeding
// resumes without the user re-opening anything. Bounded concurrency keeps the
// metadata/hash-check storm in check; failures are logged and skipped.
func resumeSeeding(s *streamer.Streamer, seeds *streamer.SeedsStore) {
	entries, err := seeds.List()
	if err != nil {
		log.Printf("Warning: resume seeding list failed: %v", err)
		return
	}
	if len(entries) == 0 {
		return
	}
	log.Printf("Seeds: resuming %d torrent(s) for seeding", len(entries))
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for _, e := range entries {
		if e.Magnet == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(e streamer.SeedEntry) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if _, aerr := s.EnsureActive(ctx, e.Magnet); aerr != nil {
				log.Printf("Seeds: resume %s (%s) failed: %v", e.Name, e.InfoHash[:min(8, len(e.InfoHash))], aerr)
			}
		}(e)
	}
	wg.Wait()
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

// initAudioMetaStore opens the DEDICATED .audio-metadata.db (kept off the
// library/history handles so a lazy tag read on a slow mount never serialises
// behind a Continue-Watching page load). Optional: a failure just disables the
// tag/cover cache (handlers fall back to live parsing), it never blocks boot.
func initAudioMetaStore(deps *appDeps) {
	amPath := deps.stateDir + "/.audio-metadata.db"
	am, err := audiometa.New(amPath)
	if err != nil {
		log.Printf("Warning: audio metadata store init failed: %v", err)
		return
	}
	deps.audioMetaStore = am
	deps.addCleanup(func() { am.Close() })
	log.Printf("Audio metadata: %s", amPath)
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
		// FileRelPath lets the store resolve files inside whole-torrent rows
		// (file_path = destination DIRECTORY) without activating the torrent.
		relPath := deps.streamSrv.FileRelPath(h, fileIdx)
		path, err := d.GetCompletedPathRel(h.HexString(), fileIdx, relPath)
		if err != nil || path == "" {
			return "", false
		}
		if st, err := os.Stat(path); err != nil || st.IsDir() {
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
			AutoPromoteArr:    q.AutoPromoteArr,
		}
	}
	worker := downloads.NewWorker(downloads.WorkerConfig{
		Store:           d,
		Streamer:        deps.streamSrv,
		DataDir:         deps.streamCfg.DataDir,
		DownloadDir:     deps.cfg.Stream.DownloadDir,
		SharedDir:       deps.cfg.Stream.SharedDir,
		Interval:        2 * time.Second,
		NtfyBaseURL:     deps.cfg.Notifications.NtfyBaseURL,
		NtfyTopic:       deps.cfg.Notifications.NtfyDefaultTopic,
		NtfyToken:       deps.cfg.Notifications.NtfyToken,
		ResolveUsername: usernameResolver,
		Settings:        queueSettings,
		Jackett:         deps.jackettClient,
		AIClient:        deps.aiClient,
		TMDBClient:      deps.tmdbClient,
		Tracker:         deps.transferTracker,
	})
	deps.downloadsWkr = worker
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

// initPushStore opens the Web Push / in-app feed store and prepares the sender
// (generating the VAPID pair on first boot). Best-effort: failure only logs —
// the app works without push.
func initPushStore(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	p, err := push.New(deps.stateDir + "/.push.db")
	if err != nil {
		log.Printf("Warning: push store init failed: %v", err)
		return
	}
	deps.pushStore = p
	deps.addCleanup(p.Close)
	sender, err := push.NewSender(p)
	if err != nil {
		log.Printf("Warning: push sender init failed: %v", err)
		return
	}
	deps.pushSender = sender
	log.Printf("Web Push: enabled (state=%s/.push.db)", deps.stateDir)
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
	if deps.downloadsStore != nil {
		worker.SetEnqueuer(deps.downloadsStore)
	}
	if deps.pushSender != nil {
		worker.SetUserNotifier(deps.pushSender)
	}
	worker.Start()
	deps.watchlistWkr = worker
	deps.addCleanup(worker.Stop)
	log.Printf("Watchlist worker: per-item scheduling (default interval=%s) default_topic=%q", interval, deps.cfg.Notifications.NtfyDefaultTopic)
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
		log.Printf("WARNING: auth disabled (JACKUI_AUTH_ENABLED!=1) — ALL endpoints are public, including admin routes (config, mounts, cache) and the Transmission RPC. Only run like this behind a trusted reverse proxy / on a private LAN; set JACKUI_AUTH_ENABLED=1 to protect them.")
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
	// Auth is enabled here (initJWTSecret only runs from initAuth). A missing/
	// short secret used to fall back to a random one per boot — which silently
	// invalidated every session on each restart (refresh tokens, MFA flows). Fail
	// fast and demand a persistent secret instead of degrading auth silently.
	if len(secret) < 32 {
		log.Fatalf("Auth: jwt_secret ausente ou curto (%d bytes) — defina jwt_secret no config ou JACKUI_JWT_SECRET com pelo menos 32 bytes; um secret efêmero desloga todas as sessões a cada restart", len(secret))
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
	hlsMgr.SetVODMode(transcode.ParseVODMode(deps.cfg.Stream.HLSVODMode))
	deps.hlsMgr = hlsMgr
	// Reap live sessions (kill ffmpeg, remove segment dirs) on shutdown instead
	// of relying on the OS to orphan the encoders.
	deps.addCleanup(hlsMgr.Stop)
}

func buildPromoteDests(cfg *config.Config) []handlers.PromoteDest {
	dests := make([]handlers.PromoteDest, 0, len(cfg.Stream.PromoteDirs)+len(cfg.External.Mounts))
	seen := map[string]bool{}
	if cfg.Stream.SharedDir != "" {
		seen[cfg.Stream.SharedDir] = true // added by BuildPromoteDests as "Biblioteca"
	}
	add := func(name, path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		dests = append(dests, handlers.PromoteDest{Name: name, Path: path})
	}
	for _, pd := range cfg.Stream.PromoteDirs {
		add(pd.Name, pd.Path)
	}
	// Writable external mounts (e.g. an rclone/GDrive mount) double as
	// promote/move targets, so a completed download or local file can be sent
	// straight there. Per-user (UserSubpath) mounts are skipped — their root
	// isn't a single destination. Writability is probed once at boot; a mount
	// that isn't mounted/writable yet simply won't be offered until a restart.
	for _, m := range cfg.External.Mounts {
		if m.UserSubpath || !dirWritable(m.Path) {
			continue
		}
		add(m.Name, m.Path)
	}
	return dests
}

// dirWritable reports whether path is a writable directory, by creating and
// removing a probe file. Best-effort — used to decide if an external mount can
// be offered as a promote destination.
func dirWritable(path string) bool {
	if path == "" {
		return false
	}
	probe := filepath.Join(path, ".jackui-wtest")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return true
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
	corsConfig.AllowAllOrigins = true
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
	api.GET("/stream/audio/meta/:hash/:file", handlers.StreamAudioMeta(deps.streamSrv))
	api.GET("/stream/subtrack/:hash/:file/:track", handlers.StreamSubtitleExtract(deps.streamSrv))
	api.GET("/stream/playlist/:hash/:file", handlers.StreamPlaylistM3U(deps.streamSrv))
	api.POST("/stream/prefetch/:hash/:file", handlers.StreamPrefetch(deps.streamSrv))
	api.GET("/stream/artwork/:hash/:file", handlers.StreamArtwork(deps.streamSrv))
	api.GET("/stream/metadata/:hash", handlers.StreamMetadata(deps.streamSrv))
	api.GET("/stream/health/:hash", handlers.StreamHealth(deps.streamSrv))
	api.GET("/stream/trackers/:hash", handlers.StreamTrackers(deps.streamSrv))
	api.GET("/stream/thumb/:hash/:file", handlers.StreamThumbnail(deps.streamSrv))
	api.GET("/stream/art/:hash", handlers.StreamArt(deps.streamSrv))
	api.POST("/stream/art/:hash/resolve", handlers.ResolveArt(deps.streamSrv, deps.tmdbClient, deps.aiClient, deps.webSearch))
	api.GET("/stream/:hash/:file", handlers.StreamFile(deps.streamSrv, deps.downloadsStore))
	api.DELETE("/stream/:hash", handlers.StreamDrop(deps.streamSrv, deps.hlsMgr))
	api.POST("/stream/:hash/viewer", handlers.StreamViewerOpen(deps.streamSrv))
	api.DELETE("/stream/:hash/viewer", handlers.StreamViewerClose(deps.streamSrv, deps.hlsMgr))
	api.GET("/stream/transcode/:hash/:file", handlers.TranscodeStream(deps.streamSrv, deps.downloadsStore))

	api.GET("/transfers", handlers.TransfersList(deps.transferTracker))

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
	d := handlers.PreviewDeps{Streamer: deps.streamSrv, Downloads: deps.downloadsStore, Local: deps.localBrowser}
	api.GET("/preview/archive", handlers.PreviewArchiveList(d))
	api.GET("/preview/archive/entry", handlers.PreviewArchiveEntry(d))
	api.GET("/preview/comic", handlers.PreviewComicManifest(d))
	api.GET("/preview/comic/page", handlers.PreviewComicPage(d))
	api.GET("/preview/epub", handlers.PreviewEpubManifest(d))
	api.GET("/preview/epub/chapter", handlers.PreviewEpubChapter(d))
	api.GET("/preview/epub/res", handlers.PreviewEpubResource(d))
}

func registerLocalRoutes(api *gin.RouterGroup, deps *appDeps) {
	api.GET("/local/mounts", handlers.LocalMounts(deps.localBrowser))
	api.GET("/local/list", handlers.LocalList(deps.localBrowser, deps.streamSrv))
	api.POST("/local/hidden", handlers.LocalSetHidden(deps.localBrowser, deps.streamSrv))
	api.GET("/local/hidden", handlers.LocalListHidden(deps.streamSrv))
	api.GET("/local/file", handlers.LocalFile(deps.localBrowser, deps.localStream, deps.localCache))
	api.GET("/local/transfer-status", handlers.LocalTransferStatus(deps.localBrowser, deps.localStream))
	api.POST("/local/cache", handlers.LocalCacheStart(deps.localBrowser, deps.localCache))
	api.POST("/local/cache/folder", handlers.LocalCacheFolder(deps.localBrowser, deps.localCache))
	api.GET("/local/cache/status", handlers.LocalCacheStatus(deps.localBrowser, deps.localCache))
	api.DELETE("/local/cache", handlers.LocalCacheDelete(deps.localBrowser, deps.localCache))
	api.GET("/local/thumb", handlers.LocalThumb(deps.localBrowser))
	api.GET("/local/transcode", handlers.LocalTranscode(deps.localBrowser))
	api.DELETE("/local/file", handlers.LocalDelete(deps.localBrowser, deps.downloadsStore, deps.streamSrv))
	api.POST("/local/clean-empty", handlers.LocalCleanEmptyDirs(deps.localBrowser))
	api.GET("/local/duplicates", handlers.LocalDuplicates(deps.localBrowser))
	api.POST("/local/duplicates/delete", handlers.LocalDuplicatesDelete(deps.localBrowser, deps.downloadsStore, deps.streamSrv))
	api.POST("/local/promote", handlers.LocalPromote(deps.localBrowser, deps.aiClient, deps.tmdbClient, deps.cfg.Stream.SharedDir, deps.promoteDests, deps.downloadsStore, deps.streamSrv, deps.transferTracker))
	api.POST("/local/promote/preview", handlers.LocalPromotePreview(deps.localBrowser, deps.aiClient, deps.tmdbClient, deps.cfg.Stream.SharedDir, deps.promoteDests))
	api.GET("/local/walk", handlers.LocalWalk(deps.localBrowser))
	api.POST("/local/move", handlers.LocalMoveEntry(deps.localBrowser, deps.downloadsStore, deps.streamSrv, deps.transferTracker))
	api.POST("/local/rename", handlers.LocalRename(deps.localBrowser, deps.downloadsStore, deps.streamSrv))
	api.POST("/local/lock", handlers.LocalSetFolderLock(deps.localBrowser))
	api.POST("/local/upload", handlers.LocalUpload(deps.localBrowser, int64(deps.cfg.External.MaxUploadMB)<<20))
	api.GET("/local/play", handlers.LocalHiddenGate(deps.streamSrv), handlers.LocalPlay(deps.localBrowser, deps.libraryStore))
	api.GET("/local/audio/meta", handlers.LocalAudioMeta(deps.localBrowser, deps.audioMetaStore))
	api.GET("/local/audio/cover", handlers.LocalAudioCover(deps.localBrowser, deps.audioMetaStore, deps.webSearch))
	api.GET("/lyrics", handlers.LyricsGet(deps.lyricsClient))
	api.GET("/music/trending", handlers.MusicTrending(deps.musicTrending))
	api.GET("/local/probe", handlers.LocalProbe(deps.localBrowser))
	api.GET("/local/sidecars", handlers.LocalSidecars(deps.localBrowser))
	api.GET("/local/sidecar", handlers.LocalSidecarRead(deps.localBrowser))
	api.GET("/local/subtrack", handlers.LocalSubtitleExtract(deps.localBrowser, deps.localCache))
	if deps.subtitleClient != nil {
		api.GET("/local/subtitles/auto", handlers.LocalSubtitlesAuto(deps.localBrowser, deps.subtitleClient))
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
	api.POST("/downloads", handlers.DownloadsCreate(deps.downloadsStore))
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
	api.POST("/downloads/:id/promote", handlers.DownloadsPromote(deps.downloadsStore, deps.streamSrv, deps.aiClient, deps.tmdbClient, deps.cfg.Stream.SharedDir, deps.promoteDests, deps.transferTracker))
	api.POST("/downloads/promote", handlers.DownloadsPromoteBatch(deps.downloadsStore, deps.streamSrv, deps.aiClient, deps.tmdbClient, deps.cfg.Stream.SharedDir, deps.promoteDests, deps.transferTracker))
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
	api.GET("/local/hls/index.m3u8", handlers.LocalHLSMaster(deps.localBrowser, deps.hlsMgr, deps.localStream, deps.localCache))
	api.GET("/local/hls/seg", handlers.LocalHLSSegment(deps.localBrowser, deps.hlsMgr))
	adminAPI.GET("/transcode/active", handlers.TranscodeActive(deps.hlsMgr))
	adminAPI.DELETE("/transcode/active/:key", handlers.TranscodeKill(deps.hlsMgr))
}

func registerTMDBRoutes(api *gin.RouterGroup, deps *appDeps) {
	// TMDB enrichment — optional poster + overview per torrent title
	api.GET("/tmdb/match", handlers.TmdbMatch(deps.tmdbClient))
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

func setupLogger() {
	var handler slog.Handler
	if os.Getenv("JACKUI_LOG_FORMAT") == "json" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)

	// Redireciona os logs do pacote standard "log" para o handler slog
	log.SetOutput(slog.NewLogLogger(handler, slog.LevelInfo).Writer())
	log.SetFlags(0)
}
