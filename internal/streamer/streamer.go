// Package streamer manages active torrents for HTTP streaming.
// Torrents stay loaded while clients are reading; idle ones are evicted.
package streamer

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	alog "github.com/anacrolix/log"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"
)

// defaultMaxEstablishedConns mirrors the anacrolix client default. We re-apply
// this on Resume() after a Pause() temporarily zeroed it.
const defaultMaxEstablishedConns = 80

// publicTrackers is a curated set of high-uptime public BitTorrent trackers
// injected into EVERY added torrent — including replays from the library /
// favorites, whose stored magnet is often just a bare infoHash with no announce
// list. DHT-only magnets report 0 peers during quiet DHT windows; announcing to
// a broad tracker set recovers peers whenever seeds exist, regardless of where
// the magnet came from. Each tier is a single-element list (announce-list format
// — each gets its own tier so the client queries all of them in parallel rather
// than failing over one-by-one). Curated from the ngosang/trackerslist "best".
var publicTrackers = [][]string{
	{"udp://tracker.opentrackr.org:1337/announce"},
	{"udp://open.tracker.cl:1337/announce"},
	{"udp://tracker.openbittorrent.com:6969/announce"},
	{"udp://exodus.desync.com:6969/announce"},
	{"udp://open.demonii.com:1337/announce"},
	{"udp://tracker.torrent.eu.org:451/announce"},
	{"udp://open.stealth.si:80/announce"},
	{"udp://tracker.tiny-vps.com:6969/announce"},
	{"udp://tracker.dler.org:6969/announce"},
	{"udp://explodie.org:6969/announce"},
	{"udp://opentracker.i2p.rocks:6969/announce"},
	{"udp://tracker1.bt.moack.co.kr:80/announce"},
	{"udp://tracker.bittor.pw:1337/announce"},
	{"udp://tracker.dump.cl:6969/announce"},
	{"udp://wepzone.net:6969/announce"},
	{"udp://retracker01-msk-virt.corbina.net:80/announce"},
	{"https://tracker.tamersunion.org:443/announce"},
	{"https://tracker.gbitt.info:443/announce"},
	{"http://tracker.openbittorrent.com:80/announce"},
	{"udp://tracker.0x7c0.com:6969/announce"},
}

// videoExtensions are formats we expose as streamable via the player.
// Anything not in this list is still available for download but won't be auto-selected.
var videoExtensions = map[string]bool{
	".mp4": true, ".m4v": true, ".mkv": true, ".webm": true,
	".avi": true, ".mov": true, ".wmv": true, ".flv": true,
	".ts": true, ".m2ts": true, ".mts": true, ".mpeg": true, ".mpg": true, ".ogv": true,
	".vob": true, ".divx": true, ".3gp": true, ".rmvb": true, ".rm": true, ".asf": true, ".f4v": true,
}

type Config struct {
	DataDir      string        // where pieces are written; subdirs per torrent
	IdleTimeout  time.Duration // drop torrent after this much inactivity (files stay)
	MetadataWait time.Duration // how long to block waiting for .torrent metadata
	MaxCacheSize int64         // total cache cap in bytes; 0 = unlimited (no eviction)
	// MaxDownloadRate caps inbound peer bandwidth in bytes/sec; 0 = unlimited.
	// Wired into the anacrolix ClientConfig.DownloadRateLimiter. Can be updated
	// at runtime via Streamer.SetRateLimits.
	MaxDownloadRate int64
	// MaxUploadRate caps outbound peer bandwidth in bytes/sec; 0 = unlimited.
	MaxUploadRate int64
	// JackettHost is the host (no port) of the configured Jackett instance. The
	// SSRF guard trusts it (Jackett lives on the private LAN, so its download
	// links are legitimately private addresses) and the apikey below is injected
	// server-side so it never has to travel through the browser.
	JackettHost   string
	JackettAPIKey string
	// ListenPort is the BitTorrent peer port for inbound connections. 0 → DefaultPeerPort.
	// Behind a VPN this should be the provider's forwarded port so peers can
	// reach us (seed + better leech). See resolvePeerPort in main.
	ListenPort int
	// ── Performance / hardware tuning (0/"" = default da lib) ──
	// Readahead é o buffer de leitura à frente por sessão de streaming, em bytes.
	// 0 → 32 MiB. Aplicado por Reader; mutável ao vivo via SetStreamReadahead.
	Readahead int64
	// StorageBackend: "file" (default, grava direto) ou "mmap" (page cache).
	// Lido só na construção do client (New) — mudar exige reiniciar o processo.
	StorageBackend string
	// Tuning de peers/CPU — só aplicados em New (exigem reinício). 0 = default
	// anacrolix (conns=50, half-open=25, peersHighWater=500, pieceHashers=2).
	MaxConnsPerTorrent int
	HalfOpenConns      int
	PeersHighWater     int
	PieceHashers       int
	// SeedTrackers lista substrings de announce URLs cujos torrents devem
	// continuar seedando após o uso (não dropados). Ver Streamer.seedTrackers.
	SeedTrackers []string
}

// FilePathResolver resolves an info_hash and file index to a local physical file path.
// Returns the file path and true if the file is completed and exists on disk.
type FilePathResolver func(hash metainfo.Hash, fileIdx int) (string, bool)

type Streamer struct {
	cfg              Config
	client           *torrent.Client
	mu               sync.Mutex
	active           map[metainfo.Hash]*entry
	favs             *FavoritesStore // optional — nil disables favorites protection
	cache            *MetadataCache  // optional — nil disables instant-open snapshots
	stop             chan struct{}
	filePathResolver FilePathResolver
	// dlPieceCompletion is a shared, persistent (Bolt) piece-completion DB used by
	// the download-to-bulk storage (downloadStorage). Persistent so a restart
	// doesn't re-hash huge files sitting on the slow bulk disk. Shared across all
	// download torrents (Bolt indexes by infohash internally). nil → NewFileOpts
	// falls back to its own default completion under each baseDir (still persists).
	dlPieceCompletion storage.PieceCompletion
	// downloads holds torrent names that are part of a background-download
	// queue. They must NOT be evicted by enforceCacheLimit even when idle —
	// the user is waiting for the file to finish. The downloads worker
	// (internal/downloads) maintains this set via RegisterDownload /
	// UnregisterDownload.
	downloads map[string]struct{}
	// metainfoDir holds serialized .torrent files keyed by info_hash so that
	// re-opening a previously-seen magnet skips the DHT metadata round-trip.
	metainfoDir string
	// verifiedFiles tracks "hash-fileIdx" keys we've already hash-checked
	// against the disk this process lifetime, so we reconcile the cache for a
	// given file exactly once (not on every FileReader call).
	verifiedMu    sync.RWMutex
	verifiedFiles map[string]bool
	// verifyLim caps concurrent piece-hash jobs (disk I/O), independent of the
	// download scheduler's max_active (peer I/O). Live-tunable via
	// SetVerifyConcurrency. nil = unlimited (tests only).
	verifyLim *verifyLimiter
	// Global bandwidth limiters wired into the anacrolix client config. Mutated
	// in place via SetLimit/SetBurst — anacrolix re-reads the limit on every
	// chunk read/write.
	dlLimiter *rate.Limiter
	upLimiter *rate.Limiter
	// storageImpl é o backend de storage quando explicitamente escolhido (mmap).
	// nil quando usamos o default FileStorage do anacrolix (gerido internamente
	// pelo client). Fechado no Close() para liberar mapeamentos/handles.
	storageImpl storage.ClientImplCloser
	// readahead é o buffer de leitura à frente por stream, em bytes. Lido sob mu;
	// mutável ao vivo via SetStreamReadahead. 0 → streamReadaheadDefault.
	readahead int64
	// Eviction observability: lifetime counters bumped by evictCandidates and
	// surfaced via Stats(), so the cache UI can show how much the LRU has been
	// reclaiming (and when it last fired). Guarded by their own mutex so the
	// eviction path doesn't contend on s.mu while deleting from disk.
	evictMu        sync.Mutex
	evictedCount   int64
	evictedBytes   int64
	lastEvictionAt time.Time
	// seedTrackers holds lower-cased substrings matched against a torrent's
	// announce URLs. A torrent whose trackers match is kept alive (seeding)
	// instead of being dropped by the idle reaper or after the last viewer
	// leaves — so private-tracker content (e.g. jackui) keeps uploading
	// and the user's ratio survives. Guarded by s.mu; mutable live via
	// SetSeedTrackers. seeds (optional) persists these hashes so seeding
	// resumes across restarts.
	seedTrackers []string
	seeds        *SeedsStore
}

// streamReadaheadDefault é o readahead de streaming quando não configurado: 32
// MiB. Calibrado para o caminho de transcode HLS — abaixo disso o Reader do
// anacrolix bloqueia esperando o próximo piece e o ffmpeg engasga.
const streamReadaheadDefault = 32 << 20

// DefaultPeerPort is the inbound BitTorrent peer port used when none is
// configured (no VPN forwarded port, no JACKUI_PEER_PORT). Exported so the
// boot wiring can treat "fell back to default" identically to the streamer.
const DefaultPeerPort = 51469

// SetFilePathResolver registers the custom file path resolver function (typically querying the downloads DB).
func (s *Streamer) SetFilePathResolver(r FilePathResolver) {
	s.filePathResolver = r
}

// HasFilePathResolver reports whether the file-path resolver has been wired yet.
// Boot-time seed resumption waits on this so relocatedStorage (which needs the
// resolver to locate moved files) doesn't lose a race against the resolver being
// set — otherwise a resumed seed would fall back to the empty cache storage and
// show 0%.
func (s *Streamer) HasFilePathResolver() bool { return s.filePathResolver != nil }

// SetFavorites attaches the favorites store. Must be called before any GC tick.
func (s *Streamer) SetFavorites(f *FavoritesStore) { s.favs = f }

// SetSeeds attaches the persistent seed store (info_hash → magnet) so torrents
// kept alive for seeding are re-added on boot. Optional — nil disables
// persistence (seeding still works in-memory until the process exits).
func (s *Streamer) SetSeeds(st *SeedsStore) { s.seeds = st }

// SetSeedTrackers replaces the live list of tracker substrings whose torrents
// must keep seeding. Applied immediately; matched case-insensitively against
// announce URLs. Safe to call at runtime (e.g. from the settings endpoint).
func (s *Streamer) SetSeedTrackers(trackers []string) {
	norm := normalizeSeedTrackers(trackers)
	s.mu.Lock()
	s.seedTrackers = norm
	s.mu.Unlock()
}

// normalizeSeedTrackers lower-cases and trims entries, dropping empties.
func normalizeSeedTrackers(in []string) []string {
	var out []string
	for _, t := range in {
		if s := strings.ToLower(strings.TrimSpace(t)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// shouldKeepSeeding reports whether the torrent belongs to a configured
// seed-tracker and must therefore be kept alive instead of dropped. Caller must
// hold s.mu (reads s.seedTrackers). Matching is a case-insensitive substring of
// any announce URL — the same announce source buildInfo surfaces to the UI.
func (s *Streamer) shouldKeepSeeding(t *torrent.Torrent) bool {
	if len(s.seedTrackers) == 0 || t == nil {
		return false
	}
	mi := t.Metainfo()
	var anns []string
	for _, tier := range mi.UpvertedAnnounceList() {
		anns = append(anns, tier...)
	}
	return matchesSeedTracker(anns, s.seedTrackers)
}

// matchesSeedTracker reports whether any announce URL contains any of the
// (already lower-cased) seed-tracker substrings. Pure helper so the matching is
// unit-testable without constructing a live *torrent.Torrent.
func matchesSeedTracker(announces, trackers []string) bool {
	for _, ann := range announces {
		la := strings.ToLower(ann)
		for _, want := range trackers {
			if want != "" && strings.Contains(la, want) {
				return true
			}
		}
	}
	return false
}

// RegisterDownload marks a torrent (by directory name == torrent.Name()) as
// part of an in-progress background download. While registered, its on-disk
// pieces are protected from enforceCacheLimit eviction even if no one is
// streaming. Idempotent.

// Client exposes the underlying anacrolix torrent client so external packages
// (the downloads worker, primarily) can resolve a hash → *torrent.Torrent and
// inspect file progress without going through the streaming-oriented helpers.
func (s *Streamer) Client() *torrent.Client { return s.client }

// EnsureActive guarantees the streamer has a torrent loaded for the given
// magnet. Wraps the regular Add() pipeline so metadata cache + favorites
// stay consistent. Returns the InfoHash for callers that need to address
// the torrent directly.
func (s *Streamer) EnsureActive(ctx context.Context, magnet string) (metainfo.Hash, error) {
	info, err := s.Add(ctx, magnet)
	if err != nil {
		return metainfo.Hash{}, err
	}
	var h metainfo.Hash
	if err := h.FromHexString(info.InfoHash); err != nil {
		return metainfo.Hash{}, fmt.Errorf("invalid hash from streamer.Add: %w", err)
	}
	return h, nil
}

// Favorites returns the attached store (may be nil). Nil-safe receiver — when
// the streamer itself failed to init (some tests / degraded boots), handlers
// can still call s.Favorites() without panicking.
func (s *Streamer) Favorites() *FavoritesStore {
	if s == nil {
		return nil
	}
	return s.favs
}

// SetMetadataCache attaches the metadata snapshot cache. Optional — when set,
// every successful Add() persists the file list so the UI can render it
// instantly next time the same info_hash is opened.
func (s *Streamer) SetMetadataCache(c *MetadataCache) { s.cache = c }

// MetadataCache returns the attached cache (may be nil).
func (s *Streamer) MetadataCache() *MetadataCache { return s.cache }

// UpdateJackettHost refreshes the trusted Jackett hostname used by the SSRF
// guard. Called after the user changes the Jackett URL via the API.
func (s *Streamer) UpdateJackettHost(rawURL string) {
	if u, err := url.Parse(rawURL); err == nil {
		s.cfg.JackettHost = u.Hostname()
	}
}

func New(cfg Config) (*Streamer, error) {
	if cfg.DataDir == "" {
		cfg.DataDir = "./streams"
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 30 * time.Minute
	}
	if cfg.MetadataWait == 0 {
		cfg.MetadataWait = 60 * time.Second
	}
	if cfg.ListenPort == 0 {
		cfg.ListenPort = DefaultPeerPort
	}

	tcfg := torrent.NewDefaultClientConfig()
	tcfg.DataDir = cfg.DataDir
	tcfg.Seed = true
	tcfg.NoUpload = false
	tcfg.ListenPort = cfg.ListenPort
	// Reduce log noise
	tcfg.Logger = tcfg.Logger.WithFilterLevel(alog.Critical)

	// Tuning de peers/CPU: só sobrescreve quando configurado (>0), senão mantém o
	// default sensato da lib. Lido só aqui — mudar exige reiniciar o processo.
	applyPeerTuning(tcfg, cfg)

	// Storage backend: mmap mapeia os arquivos em memória (page cache) p/ seek mais
	// rápido; file (default) grava direto. Guardamos o closer p/ liberar no Close().
	var storageImpl storage.ClientImplCloser
	if cfg.StorageBackend == "mmap" {
		storageImpl = storage.NewMMap(cfg.DataDir)
		tcfg.DefaultStorage = storageImpl
	}

	// Build always-on rate limiters so callers can dynamically adjust the cap at
	// runtime. anacrolix only reads the limiter pointer once (at client
	// construction), so SetLimit/SetBurst on the same instance is the only way
	// to change limits without restarting the client. Inf means unlimited.
	dlLimit := rateFromBytes(cfg.MaxDownloadRate)
	upLimit := rateFromBytes(cfg.MaxUploadRate)
	dlLimiter := rate.NewLimiter(dlLimit, rateBurst(cfg.MaxDownloadRate))
	upLimiter := rate.NewLimiter(upLimit, rateBurst(cfg.MaxUploadRate))
	tcfg.DownloadRateLimiter = dlLimiter
	tcfg.UploadRateLimiter = upLimiter

	client, err := torrent.NewClient(tcfg)
	if err != nil {
		return nil, fmt.Errorf("torrent client: %w", err)
	}

	// Per-torrent metainfo cache lives next to the pieces. Storing the parsed
	// .torrent file lets a future Add() skip the DHT round-trip entirely —
	// anacrolix can start downloading the moment the client knows the piece
	// hashes. Without this, even a magnet we've seen 10 times waits ~3-10s for
	// peers + DHT to deliver the metadata anew every cold-start.
	metainfoDir := filepath.Join(cfg.DataDir, ".metainfo")
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	_ = os.MkdirAll(metainfoDir, 0o755)

	// Shared persistent piece-completion DB for download-to-bulk storage. Lives in
	// the cache dir (it's only piece metadata — KB/MB), at a path DISTINCT from the
	// client's own completion DB so the two Bolt files never lock each other.
	dlCompletionDir := filepath.Join(cfg.DataDir, ".piece-completion-dl")
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	_ = os.MkdirAll(dlCompletionDir, 0o755)
	dlPieceCompletion, err := storage.NewBoltPieceCompletion(dlCompletionDir)
	if err != nil {
		log.Printf("streamer: download piece-completion DB unavailable (%v) — falling back to per-baseDir completion", err)
		dlPieceCompletion = nil
	}

	s := &Streamer{
		cfg:               cfg,
		client:            client,
		active:            make(map[metainfo.Hash]*entry),
		stop:              make(chan struct{}),
		downloads:         make(map[string]struct{}),
		metainfoDir:       metainfoDir,
		verifiedFiles:     make(map[string]bool),
		verifyLim:         newVerifyLimiter(1),
		dlLimiter:         dlLimiter,
		upLimiter:         upLimiter,
		storageImpl:       storageImpl,
		readahead:         cfg.Readahead,
		seedTrackers:      normalizeSeedTrackers(cfg.SeedTrackers),
		dlPieceCompletion: dlPieceCompletion,
	}

	go s.gcLoop()
	return s, nil
}

// applyPeerTuning sobrescreve os limites de conexão/peers/hashers do ClientConfig
// quando configurados (>0). Valores 0 preservam o default da lib anacrolix.
func applyPeerTuning(tcfg *torrent.ClientConfig, cfg Config) {
	if cfg.MaxConnsPerTorrent > 0 {
		tcfg.EstablishedConnsPerTorrent = cfg.MaxConnsPerTorrent
	}
	if cfg.HalfOpenConns > 0 {
		tcfg.HalfOpenConnsPerTorrent = cfg.HalfOpenConns
	}
	if cfg.PeersHighWater > 0 {
		tcfg.TorrentPeersHighWater = cfg.PeersHighWater
	}
	if cfg.PieceHashers > 0 {
		tcfg.PieceHashersPerTorrent = cfg.PieceHashers
	}
}

// streamReadahead retorna o readahead de streaming em bytes (configurado ou default).
func (s *Streamer) streamReadahead() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readahead > 0 {
		return s.readahead
	}
	return streamReadaheadDefault
}

// SetStreamReadahead atualiza ao vivo o readahead de streaming (em MB). Vale a
// partir do próximo Reader aberto. mb<=0 volta ao default. Não exige reinício.
func (s *Streamer) SetStreamReadahead(mb int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mb <= 0 {
		s.readahead = 0
		return
	}
	s.readahead = int64(mb) << 20
}

// StreamReadaheadForTesting expõe o readahead efetivo (em bytes) para testes de
// outros pacotes verificarem que um setter foi aplicado.
func (s *Streamer) StreamReadaheadForTesting() int64 { return s.streamReadahead() }
