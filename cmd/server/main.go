package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/audiometa"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	appdb "github.com/lgldsilva/jackui/internal/db"
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
	jackuimdns "github.com/lgldsilva/jackui/internal/mdns"
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

// instanceLockName is the lockfile (inside DataDir) backing the single-instance
// guard. It carries no data — only the flock matters.
const instanceLockName = ".jackui.lock"

// acquireInstanceLock takes an exclusive non-blocking flock on a lockfile
// inside dataDir, so a second jackui process pointed at the same DataDir fails
// fast instead of corrupting the anacrolix bolt DB / piece storage (neither is
// multi-process safe). Returns (nil, nil) for an empty dataDir — nothing to
// protect (tests/CI configs without a DataDir). The lock lives as long as the
// returned file stays open; closing it (or process exit) releases it.
// startDiscovery wires the optional LAN discovery (_jackui._tcp, issue #201
// item 6): opt-in via JACKUI_MDNS=1 — prod runs in gluetun's netns where
// multicast cannot reach the LAN, so off by default and never blocking
// startup/shutdown. Extracted from main so the wiring is unit-testable.
func startDiscovery(deps *appDeps) {
	stopMDNS := jackuimdns.Announce("jackui", deps.cfg.Port,
		jackuimdns.BaseURLForAnnounce(deps.cfg.BaseURL, deps.cfg.Port))
	deps.addCleanup(stopMDNS)
}

func acquireInstanceLock(dataDir string) (*os.File, error) {
	if dataDir == "" {
		return nil, nil
	}
	// #nosec G301 -- data dir; 0755 intentional so media tools can traverse it
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	// #nosec G304 -- path derived from the configured DataDir
	f, err := os.OpenFile(filepath.Join(dataDir, instanceLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open instance lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another jackui instance is running on this DataDir (%s): %w", dataDir, err)
	}
	return f, nil
}

type appDeps struct {
	cfg            *config.Config
	configPath     string
	db             *sql.DB              // shared PostgreSQL pool (all stores)
	dbProbe        *appdb.ReadOnlyProbe // edge only: pool fell back to a read-only standby
	jackettClient  *jackett.Client
	localBrowser   *local.Browser
	historyStore   *history.Store
	streamSrv      *streamer.Streamer
	streamCfg      streamer.Config
	libraryStore   *library.Store
	audioMetaStore *audiometa.Store
	lyricsClient   *lyrics.Client
	musicTrending  *musictrending.Client
	playlistsStore *playlists.Store
	downloadsStore *downloads.Store
	downloadsWkr   *downloads.Worker
	tmdbClient     *tmdb.Client
	aiClient       *ai.Client
	aiBench        *ai.BenchmarkStore
	aiBenchRun     *handlers.BenchmarkRunTracker
	webSearch      *imagesearch.Chain
	watchlistStore *watchlist.Store
	watchlistWkr   *watchlist.Worker
	pushStore      *push.Store
	pushSender     *push.Sender
	subtitleClient *subtitles.Client
	// Auto-subtitle pipeline (constructed in wiring.go initSubtitlesAuto; nil
	// when the streamer/DB pool are unavailable — handlers nil-guard it).
	subtitlesAuto    *subtitles.AutoFetcher
	subtitleLinks    *subtitles.LinksStore
	subtitleAutoMeta subtitles.TorrentMeta
	authStore        *auth.Store
	tokenMgr         *auth.TokenManager
	waManager        *auth.WAManager
	loginLockout     *auth.Lockout
	deviceFlow       *auth.DeviceFlow
	deviceLockout    *auth.Lockout
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
		log.Printf("cleanup exceeded %s (anacrolix/DHT stuck, network down?) — forcing exit; the next boot reconciles", cleanupHardDeadline)
		os.Exit(0)
	}
}

// setSoftMemoryLimit applies a soft heap limit to the garbage collector
// (runtime/debug.SetMemoryLimit) from JACKUI_GOMEMLIMIT. On small hosts
// (Raspberry Pi, containers with a RAM ceiling) the default GC only reacts to
// the GOGC ratio and can let the heap grow until the OOM-killer steps in; the
// soft limit makes the GC intensify before the ceiling. Accepted formats: "off"
// (disables), integer bytes ("536870912") or a binary suffix KiB/MiB/GiB
// ("512MiB"). Empty/invalid = Go default behaviour.
func setSoftMemoryLimit(v string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return
	}
	if strings.EqualFold(v, "off") {
		debug.SetMemoryLimit(-1)
		slog.Info("JACKUI_GOMEMLIMIT=off — soft limit disabled")
		return
	}
	limit, ok := parseMemLimit(v)
	if !ok {
		slog.Warn("JACKUI_GOMEMLIMIT invalid — ignored", "value", v,
			"expected", "integer bytes or a KiB/MiB/GiB suffix")
		return
	}
	prev := debug.SetMemoryLimit(limit)
	if prev == math.MaxInt64 {
		slog.Info("soft memory limit aplicado", "limit", limit)
	} else {
		slog.Info("soft memory limit updated", "limit", limit, "previous", prev)
	}
}

func parseMemLimit(s string) (int64, bool) {
	lower := strings.ToLower(s)
	units := []struct {
		suffix string
		mult   int64
	}{
		{"kib", 1 << 10}, {"mib", 1 << 20}, {"gib", 1 << 30},
		{"k", 1 << 10}, {"m", 1 << 20}, {"g", 1 << 30},
	}
	for _, u := range units {
		if strings.HasSuffix(lower, u.suffix) {
			n, err := strconv.ParseInt(strings.TrimSuffix(lower, u.suffix), 10, 64)
			if err != nil || n <= 0 {
				return 0, false
			}
			return n * u.mult, true
		}
	}
	n, err := strconv.ParseInt(lower, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func main() {
	// Runs every 30s under Docker: keep it ahead of any logger/runtime setup.
	if handledHealthcheck() {
		return
	}
	setupLogger()
	setSoftMemoryLimit(os.Getenv("JACKUI_GOMEMLIMIT"))
	// Must run before loadConfig, which treats os.Args[1] as the config path.
	if err := removedSubcommand(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
	deps := bootstrapApp()
	defer deps.runCleanup()

	gin.SetMode(gin.ReleaseMode)
	router := setupRouter(deps)
	distFS := mustGetDistFS()
	router.NoRoute(spaFallback(distFS, http.FileServer(http.FS(distFS))))

	addr := fmt.Sprintf(":%d", deps.cfg.Port)
	log.Printf("JackUI starting on http://localhost%s", addr)
	startDiscovery(deps)
	// ReadHeaderTimeout bounds how long a client may take to send request
	// headers — without it a slow-loris connection can hold a goroutine open
	// indefinitely (gosec G112). MaxHeaderBytes caps header size (default 1MB
	// made explicit). IdleTimeout recycles keep-alive connections gone silent.
	// ReadTimeout/WriteTimeout deliberately stay 0: media streaming, SSE and
	// long transcode responses legitimately run for minutes.
	// WithAPIv1Alias serves the /api/v1 prefix native clients pin (issue #201).
	srv := &http.Server{
		Addr:              addr,
		Handler:           WithAPIv1Alias(router),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	serveUntilShutdown(deps, srv)
}

// removedSubcommand rejects the old `jackui migrate-auth` subcommand with a
// pointer to its new home, instead of loading "migrate-auth" as a config path.
func removedSubcommand(args []string) error {
	if len(args) > 0 && args[0] == "migrate-auth" {
		return errors.New("migrate-auth is no longer part of the server binary; " +
			"build and run the standalone command: go run ./cmd/migrate-auth --from <auth.db> --to <DSN>")
	}
	return nil
}

// bootstrapApp wires config, stores, workers and destinations.
func bootstrapApp() *appDeps {
	deps := &appDeps{}
	deps.cfg, deps.configPath = loadConfig()
	log.Printf("Node role: %s", deps.cfg.NodeRoleName())
	// Single-instance guard: two processes on one DataDir would race on the
	// anacrolix bolt DB and piece storage. Fail fast; the running instance wins.
	lock, err := acquireInstanceLock(deps.cfg.Stream.DataDir)
	if err != nil {
		log.Fatalf("%v", err)
	}
	if lock != nil {
		deps.addCleanup(func() { _ = lock.Close() }) // releases the flock
	}
	if err := config.CheckWritable(deps.configPath); err != nil {
		log.Printf("WARNING: config %s is not writable (%v) — changes from Settings/Mounts will not persist; fix the owner/permissions on the host for the container uid", deps.configPath, err)
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
	initSubtitlesAuto(deps)
	initAuth(deps)
	migrateUserSubpathMounts(deps)
	deps.promoteDests = buildPromoteDests(deps.cfg)
	deps.destinations = newDestinationService(deps)
	initHLSManager(deps)
	if deps.cfg.IsEdge() {
		// The boot purge wipes EVERY user's incognito rows — on an edge that would
		// kill sessions live on the primary. Keep only the TTL loop for its own.
		handlers.StartIncognitoReaperLoop(deps.historyStore, deps.libraryStore)
	} else {
		handlers.StartIncognitoReaper(deps.historyStore, deps.libraryStore)
	}
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
			u, err := deps.authStore.GetUserByID(context.Background(), userID)
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
		deps.runCleanup() // explicit before fatal (defer does not run on log.Fatalf)
		log.Fatalf("HTTP server failed: %v", err)
	case sig := <-quit:
		log.Printf("Signal %s received — graceful shutdown started...", sig)
	case <-deps.restart:
		log.Printf("VPN forwarded port changed — graceful shutdown for rebind (restart policy recreates the process)...")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown error: %v", err)
	}
	waitInFlightTransfers(deps)
	log.Printf("HTTP server stopped — running cleanups (anacrolix, stores, worker)...")
}

// waitInFlightTransfers gives active moves a bounded drain window before stores close.
func waitInFlightTransfers(deps *appDeps) {
	n := deps.transferTracker.ActiveCount()
	if n == 0 {
		return
	}
	log.Printf("Waiting for %d in-flight transfer(s) (up to 20s)...", n)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer waitCancel()
	if deps.transferTracker.WaitIdle(waitCtx) {
		log.Printf("Transfers completed.")
		return
	}
	log.Printf("Timeout — %d transfer(s) still active; they will be resumed on the next boot.", deps.transferTracker.ActiveCount())
}
