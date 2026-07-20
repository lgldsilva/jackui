package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/audiometa"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	lh "github.com/lgldsilva/jackui/internal/handlers/local"
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
	"github.com/lgldsilva/jackui/internal/musictrending"
	"github.com/lgldsilva/jackui/internal/playlists"
	"github.com/lgldsilva/jackui/internal/push"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
	"github.com/lgldsilva/jackui/internal/tmdb"
	"github.com/lgldsilva/jackui/internal/transcode"
	"github.com/lgldsilva/jackui/internal/transfer"
	"github.com/lgldsilva/jackui/internal/watchlist"
)

const (
	routeLibraryID  = "/library/:id"
	routePlaylistID = "/playlists/:id"
)

type appDeps struct {
	cfg              *config.Config
	configPath       string
	db               *sql.DB // shared PostgreSQL pool (all stores)
	jackettClient    *jackett.Client
	localBrowser     *local.Browser
	historyStore     *history.Store
	streamSrv        *streamer.Streamer
	streamCfg        streamer.Config
	libraryStore     *library.Store
	audioMetaStore   *audiometa.Store
	lyricsClient     *lyrics.Client
	musicTrending    *musictrending.Client
	playlistsStore   *playlists.Store
	downloadsStore   *downloads.Store
	downloadsWkr     *downloads.Worker
	tmdbClient       *tmdb.Client
	aiClient         *ai.Client
	aiBench          *ai.BenchmarkStore
	aiBenchRun       *handlers.BenchmarkRunTracker
	webSearch        *imagesearch.Chain
	watchlistStore   *watchlist.Store
	watchlistWkr     *watchlist.Worker
	pushStore        *push.Store
	pushSender       *push.Sender
	subtitleClient   *subtitles.Client
	authStore        *auth.Store
	tokenMgr         *auth.TokenManager
	waManager        *auth.WAManager
	loginLockout     *auth.Lockout
	authRateLimiter  *auth.IPRateLimiter
	mlr              *mailer.Mailer
	promoteDests     []httpshared.PromoteDest
	destinations     *handlers.DestinationService
	hlsMgr           *transcode.HLSSessionManager
	localStream      *localstream.Registry
	localCache       *localcache.Cache
	transferTracker  *transfer.Tracker
	pendingTransfers *transfer.Store // persisted move/promote intents → resumed on boot
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

// cleanupHardDeadline bounds graceful cleanup. The cleanups (anacrolix client
// Close → Drop each torrent + DHT, store Close, worker Stop) normally finish in
// a few seconds, but a torrent/DHT teardown can BLOCK indefinitely when the VPN
// network is down (the "error announcing to DHT: nothing resolved" flood). When
// that happened mid-shutdown the process hung forever — HTTP already down (502),
// but never exited, so `restart: unless-stopped` never recreated it. Bounding it
// + forcing os.Exit lets Docker recycle into a fresh, working instance; the next
// boot reconciles state (RescueStuckMoving, resumeSeeding, piece verify).
const cleanupHardDeadline = 20 * time.Second

func (d *appDeps) runCleanup() {
	done := make(chan struct{})
	go func() {
		for i := len(d.cleanup) - 1; i >= 0; i-- {
			d.cleanup[i]()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(cleanupHardDeadline):
		log.Printf("cleanup excedeu %s (anacrolix/DHT travado, rede caída?) — forçando saída; o próximo boot reconcilia", cleanupHardDeadline)
		os.Exit(0)
	}
}

func main() {
	setupLogger()
	// Subcommands must be handled before loadConfig (which treats os.Args[1] as
	// the config path).
	if handledMigrateAuth() {
		return
	}
	deps := bootstrapApp()
	defer deps.runCleanup()

	gin.SetMode(gin.ReleaseMode)
	router := setupRouter(deps)
	distFS := mustGetDistFS()
	router.NoRoute(spaFallback(distFS, http.FileServer(http.FS(distFS))))

	addr := fmt.Sprintf(":%d", deps.cfg.Port)
	log.Printf("JackUI starting on http://localhost%s", addr)
	// ReadHeaderTimeout bounds how long a client may take to send request
	// headers — without it a slow-loris connection can hold a goroutine open
	// indefinitely (gosec G112). Body/handler timeouts stay off: media streaming
	// and long transcode reads legitimately run for minutes.
	srv := &http.Server{Addr: addr, Handler: router, ReadHeaderTimeout: 30 * time.Second}
	serveUntilShutdown(deps, srv)
}

// handledMigrateAuth runs the migrate-auth subcommand when requested.
// Returns true when main should exit (subcommand handled, success or fatal).
func handledMigrateAuth() bool {
	if len(os.Args) <= 1 || os.Args[1] != "migrate-auth" {
		return false
	}
	if err := runMigrateAuth(os.Args[2:]); err != nil {
		log.Fatalf("migrate-auth: %v", err)
	}
	return true
}

// bootstrapApp wires config, stores, workers and destinations.
func bootstrapApp() *appDeps {
	deps := &appDeps{}
	deps.cfg, deps.configPath = loadConfig()
	if err := config.CheckWritable(deps.configPath); err != nil {
		log.Printf("WARNING: config %s não é gravável (%v) — alterações em Settings/Mounts não vão persistir; ajuste dono/permissão no host para o uid do container", deps.configPath, err)
	}
	deps.jackettClient = jackett.New(deps.cfg.Jackett.URL, deps.cfg.Jackett.APIKey)
	deps.localBrowser = local.NewBrowser(deps.cfg.External.Mounts)
	deps.localStream = localstream.NewRegistry(deps.cfg.External.LocalReadaheadMB)
	deps.addCleanup(deps.localStream.Close)
	// Global move/copy progress tracker, shared by the post-download move (worker)
	// and the Local-tab/promote/AI moves (handlers) → the Transfers dock.
	deps.transferTracker = transfer.New(deps.cfg.Stream.MaxConcurrentTransfers)
	deps.webSearch = imagesearch.Default()
	deps.mlr = mailer.New(deps.cfg.SMTP)
	deps.restart = make(chan struct{}, 1)

	initDB(deps)
	initHistoryStore(deps)
	deps.streamCfg = prepareStreamConfig(deps.cfg, deps.restart)
	// Persist local-file thumbnails under stream DataDir so they survive restarts.
	lh.SetLocalThumbCacheDir(filepath.Join(deps.streamCfg.DataDir, ".thumbs", "local"))
	initLocalCache(deps)
	initStreamer(deps)
	initLibraryStore(deps)
	initAudioMetaStore(deps)
	deps.lyricsClient = lyrics.New()
	deps.musicTrending = musictrending.New()
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
	deps.destinations = newDestinationService(deps)
	initHLSManager(deps)
	handlers.StartIncognitoReaper(deps.historyStore, deps.libraryStore)
	startStreamWorkers(deps)
	startTranscodeProbe()
	return deps
}

func initLocalCache(deps *appDeps) {
	// Dedicated cache for pre-fetching whole files from slow mounts (rclone).
	cache, err := localcache.New(filepath.Join(deps.streamCfg.DataDir, "local-cache"), deps.cfg.External.LocalCacheGB)
	if err != nil {
		log.Printf("Warning: local cache init failed: %v — local caching disabled", err)
		return
	}
	deps.localCache = cache
	deps.addCleanup(cache.Close)
}

func newDestinationService(deps *appDeps) *handlers.DestinationService {
	return &handlers.DestinationService{
		Mounts:    deps.cfg.External.Mounts,
		Promote:   deps.promoteDests,
		SharedDir: deps.cfg.Stream.SharedDir,
		ResolveUser: func(userID int) string {
			if deps.authStore == nil {
				return ""
			}
			u, err := deps.authStore.GetUserByID(userID)
			if err != nil || u == nil {
				return ""
			}
			return u.Username
		},
	}
}

func startStreamWorkers(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	// Cancellable so graceful shutdown stops these loops before stores close.
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	deps.addCleanup(cancelWorkers)
	metrics.StartWorker(workerCtx, deps.streamSrv, deps.hlsMgr)
	streamer.StartBandwidthScheduler(workerCtx, deps.streamSrv, deps.cfg)
}

// serveUntilShutdown listens, waits for stop signal/restart, then drains transfers.
func serveUntilShutdown(deps *appDeps, srv *http.Server) {
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
		deps.runCleanup() // explícito antes de fatal (defer não roda em log.Fatalf)
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
	waitInFlightTransfers(deps)
	log.Printf("HTTP server encerrado — rodando cleanups (anacrolix, stores, worker)...")
}

// waitInFlightTransfers gives active moves a bounded drain window before stores close.
func waitInFlightTransfers(deps *appDeps) {
	n := deps.transferTracker.ActiveCount()
	if n == 0 {
		return
	}
	log.Printf("Aguardando %d transferência(s) em andamento (até 20s)...", n)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer waitCancel()
	if deps.transferTracker.WaitIdle(waitCtx) {
		log.Printf("Transferências concluídas.")
		return
	}
	log.Printf("Timeout — %d transferência(s) ainda ativa(s); serão retomadas no próximo boot.", deps.transferTracker.ActiveCount())
}
