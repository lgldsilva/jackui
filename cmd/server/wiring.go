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

	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/gluetun"
	"github.com/lgldsilva/jackui/internal/handlers"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
	"github.com/lgldsilva/jackui/internal/tmdb"
	"github.com/lgldsilva/jackui/internal/transcode"
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
				// #nosec G706 -- falso-positivo: loga inteiro (%d), sem injecao de log possivel
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
			// #nosec G706 -- falso-positivo: loga inteiro (%d), sem injecao de log possivel
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
	// Piece-hash concurrency (disk) is independent of max_active (peer downloads).
	s.SetVerifyConcurrency(deps.cfg.DownloadsQueue.MaxConcurrentVerify)
	log.Printf("Streamer ready: %s (idle=%s, metadata=%s, verifyConcurrency=%d)",
		deps.streamCfg.DataDir, deps.streamCfg.IdleTimeout, deps.streamCfg.MetadataWait,
		s.VerifyConcurrency())

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

func buildPromoteDests(cfg *config.Config) []httpshared.PromoteDest {
	dests := make([]httpshared.PromoteDest, 0, len(cfg.Stream.PromoteDirs)+len(cfg.External.Mounts))
	seen := map[string]bool{}
	if cfg.Stream.SharedDir != "" {
		seen[cfg.Stream.SharedDir] = true // added by BuildPromoteDests as "Biblioteca"
	}
	add := func(name, path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		dests = append(dests, httpshared.PromoteDest{Name: name, Path: path})
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
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
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
