package main

import (
	"context"
	"log"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/audiometa"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	appdb "github.com/lgldsilva/jackui/internal/db"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/gluetun"
	"github.com/lgldsilva/jackui/internal/handlers"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/playlists"
	"github.com/lgldsilva/jackui/internal/push"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
	"github.com/lgldsilva/jackui/internal/tmdb"
	"github.com/lgldsilva/jackui/internal/transcode"
	"github.com/lgldsilva/jackui/internal/transfer"
	"github.com/lgldsilva/jackui/internal/watchlist"
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
	store, err := history.New(deps.db)
	if err != nil {
		log.Printf("Warning: failed to open history store: %v — history disabled", err)
		return
	}
	deps.historyStore = store
	log.Printf("History store: PostgreSQL")
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

func prepareStreamConfig(cfg *config.Config, restart chan<- struct{}) streamer.Config {
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
	return sc
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

	if favs, ferr := streamer.NewFavorites(deps.db); ferr == nil {
		s.SetFavorites(favs)
		deps.addCleanup(func() { favs.Close() })
		log.Printf("Favorites: PostgreSQL")
	} else {
		log.Printf("Warning: favorites store init failed: %v", ferr)
	}
	if mc, mcerr := streamer.NewMetadataCache(deps.db); mcerr == nil {
		s.SetMetadataCache(mc)
		deps.addCleanup(func() { _ = mc.Close() })
		log.Printf("Metadata cache: PostgreSQL")
	} else {
		log.Printf("Warning: metadata cache init failed: %v", mcerr)
	}
	go recoverFavorites(s, deps.jackettClient)
	if seeds, serr := streamer.NewSeeds(deps.db); serr == nil {
		s.SetSeeds(seeds)
		deps.addCleanup(func() { _ = seeds.Close() })
		log.Printf("Seeds store: PostgreSQL")
		go resumeSeeding(s, seeds)
	} else {
		log.Printf("Warning: seeds store init failed: %v", serr)
	}
}

// recoverFavoritesLimit caps the camada-3 (Jackett re-search) re-links per boot.
const recoverFavoritesLimit = 25

// recoverFavorites repairs favorites whose magnet went missing (the inert-row
// bug). Layers 1-2 are deterministic, network-free and always run. Layer 3
// re-searches the remainder on Jackett (best-effort, bounded) unless
// JACKUI_RECOVER_FAVORITES=0 or Jackett is unconfigured.
func recoverFavorites(s *streamer.Streamer, jc *jackett.Client) {
	favs := s.Favorites()
	if favs == nil {
		return
	}
	if n, err := favs.ReconcileMagnets(); err != nil {
		log.Printf("favorites recovery (deterministic) failed: %v", err)
	} else if n > 0 {
		log.Printf("favorites recovery: re-linked %d magnet(s) from info_hash/metadata", n)
	}
	if os.Getenv("JACKUI_RECOVER_FAVORITES") == "0" || jc == nil {
		return
	}
	if n, err := favs.RecoverViaSearch(jackettMagnetSearcher{jc}, recoverFavoritesLimit); err != nil {
		log.Printf("favorites recovery (Jackett re-search) failed: %v", err)
	} else if n > 0 {
		log.Printf("favorites recovery: re-linked %d magnet(s) via Jackett re-search", n)
	}
}

// jackettMagnetSearcher adapts the Jackett client to streamer.MagnetSearcher for
// the camada-3 favorites recovery.
type jackettMagnetSearcher struct{ c *jackett.Client }

func (a jackettMagnetSearcher) SearchByName(name string) ([]streamer.MagnetMatch, error) {
	res, err := a.c.Search(name, "", nil)
	if err != nil {
		return nil, err
	}
	out := make([]streamer.MagnetMatch, 0, len(res))
	for _, r := range res {
		out = append(out, streamer.MagnetMatch{
			Title: r.Title, Magnet: r.MagnetURI, InfoHash: r.InfoHash, Seeders: r.Seeders,
		})
	}
	return out, nil
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
	// Wait for the file-path resolver (wired later, during downloads init) so a
	// completed seed activates onto its relocated bulk storage instead of racing
	// ahead and falling back to the empty cache storage (which shows 0%).
	for i := 0; i < 100 && !s.HasFilePathResolver(); i++ {
		time.Sleep(150 * time.Millisecond)
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
	l, err := library.New(deps.db)
	if err != nil {
		log.Printf("Warning: library store init failed: %v", err)
		return
	}
	deps.libraryStore = l
	deps.addCleanup(func() { l.Close() })
	log.Printf("Library: PostgreSQL")
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
	am, err := audiometa.New(deps.db)
	if err != nil {
		log.Printf("Warning: audio metadata store init failed: %v", err)
		return
	}
	deps.audioMetaStore = am
	deps.addCleanup(func() { am.Close() })
	log.Printf("Audio metadata: PostgreSQL")
}

func initPlaylistsStore(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	p, err := playlists.New(deps.db)
	if err != nil {
		log.Printf("Warning: playlists store init failed: %v", err)
		return
	}
	deps.playlistsStore = p
	deps.addCleanup(func() { p.Close() })
	log.Printf("Playlists: PostgreSQL")
}

func initDownloadsStore(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	d, err := downloads.New(deps.db)
	if err != nil {
		log.Printf("Warning: downloads store init failed: %v", err)
		return
	}
	deps.downloadsStore = d
	deps.addCleanup(func() { d.Close() })
	log.Printf("Downloads: PostgreSQL")

	// Pending-transfers store: persists move/promote copy intents so a deploy or
	// crash mid-copy resumes them on the next boot (resume-aware copy finishes
	// only what's left). Best-effort — a failure here just disables resume.
	if ts, err := transfer.OpenStore(deps.db); err != nil {
		log.Printf("Warning: pending-transfers store init failed (resume disabled): %v", err)
	} else {
		deps.pendingTransfers = ts
		deps.addCleanup(func() { ts.Close() })
		handlers.ReconcilePendingTransfers(ts, deps.transferTracker, d, deps.streamSrv)
	}

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
		FallbackUser:    deps.cfg.Auth.AdminUsername,
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
	if res.Conflicts > 0 {
		// A loose root entry couldn't be scoped because <user>/<name> already exists.
		// We no longer mint "name (1)" dups, so this just means a manual merge is due.
		log.Printf("UserSubpath migration %q: %d entr(ies) left at root — per-user dest already exists (manual merge needed)", mount, res.Conflicts)
	}
	if len(res.Moved) == 0 {
		return
	}
	log.Printf("UserSubpath migration %q: moved %d entr(ies), %d already scoped, %d active (.part, left in place)", mount, len(res.Moved), res.Skipped, res.Active)
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
	tc, err := tmdb.New(deps.cfg.TMDB.APIKey, deps.cfg.TMDB.OMDbAPIKey, deps.db)
	if err != nil {
		log.Printf("Warning: tmdb client init failed: %v", err)
		return
	}
	deps.tmdbClient = tc
	deps.addCleanup(func() { _ = tc.Close() })
	if deps.cfg.TMDB.APIKey != "" {
		log.Printf("TMDB enrichment: enabled (PostgreSQL cache)")
	} else {
		log.Printf("TMDB enrichment: disabled (no API key)")
	}
}

func initAIClient(deps *appDeps) {
	aiClient := ai.New(deps.cfg.AI)
	deps.aiClient = aiClient
	if aiClient == nil {
		log.Printf("AI title identification: disabled (no chain) — using regex title cleaning")
		return
	}
	deps.aiBenchRun = handlers.NewBenchmarkRunTracker()
	bs, err := ai.NewBenchmarkStore(deps.db)
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
	p, err := push.New(deps.db)
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
	log.Printf("Web Push: enabled (PostgreSQL)")
}

func initWatchlistStore(deps *appDeps) {
	if deps.streamSrv == nil {
		return
	}
	w, err := watchlist.New(deps.db)
	if err != nil {
		log.Printf("Warning: watchlist store init failed: %v", err)
		return
	}
	deps.watchlistStore = w
	deps.addCleanup(func() { w.Close() })
	log.Printf("Watchlist: PostgreSQL")
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

// initDB opens the shared PostgreSQL pool and applies the unified schema. All
// stores receive this pool. Fatal if DATABASE_URL is unset or unreachable.
func initDB(deps *appDeps) {
	if deps.cfg.DatabaseURL == "" {
		log.Fatalf("DATABASE_URL ausente — defina JACKUI_DATABASE_URL (ou DATABASE_URL / JACKUI_PG_*) com o DSN do PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := appdb.Open(ctx, deps.cfg.DatabaseURL, 60*time.Second)
	if err != nil {
		log.Fatalf("PostgreSQL init failed: %v", err)
	}
	if err := appdb.Migrate(pool); err != nil {
		log.Fatalf("PostgreSQL migrate failed: %v", err)
	}
	deps.db = pool
	deps.addCleanup(func() { _ = pool.Close() })
	log.Printf("PostgreSQL pool ready; schema migrated")
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
	authStore, err := auth.New(deps.db)
	if err != nil {
		log.Fatalf("Auth store init failed: %v", err)
	}
	deps.authStore = authStore
	deps.addCleanup(func() { authStore.Close() })
	log.Printf("Auth enabled: user store on PostgreSQL")
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
