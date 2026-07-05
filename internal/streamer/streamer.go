// Package streamer manages active torrents for HTTP streaming.
// Torrents stay loaded while clients are reading; idle ones are evicted.
package streamer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
func (s *Streamer) RegisterDownload(name string) {
	if name == "" {
		return
	}
	s.mu.Lock()
	if s.downloads == nil {
		s.downloads = make(map[string]struct{})
	}
	s.downloads[name] = struct{}{}
	s.mu.Unlock()
}

// UnregisterDownload removes the eviction protection. Call after the
// download completes or is cancelled. Idempotent.
func (s *Streamer) UnregisterDownload(name string) {
	s.mu.Lock()
	delete(s.downloads, name)
	s.mu.Unlock()
}

// IsDownloadProtected reports whether `name` is currently in the download
// protection set. Used by tests + the cache eviction code.
//
// anacrolix grava arquivos SINGLE-FILE como "<name>.part" enquanto o download
// não terminou — ao mesmo tempo `t.Name()` (que o worker registra) NÃO inclui
// o sufixo. Sem essa tolerância o enforceCacheLimit passa "<name>.part" e
// consulta um set que só tem "<name>", então conclui que o arquivo NÃO está
// protegido e o LRU deleta o .part — anacrolix perde os pieces no disco e
// recomeça do zero. (Multi-file torrents não sofrem porque o entry é o
// diretório, cujo nome casa com t.Name() exatamente.)
func (s *Streamer) IsDownloadProtected(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.downloads[name]; ok {
		return true
	}
	if stripped := strings.TrimSuffix(name, ".part"); stripped != name {
		if _, ok := s.downloads[stripped]; ok {
			return true
		}
	}
	return false
}

// evictionBlocked reports whether a top-level DataDir entry `name` must be kept
// RIGHT NOW because it belongs to a currently-loaded torrent or a protected
// download. Re-checked under the lock immediately before deletion to close the
// TOCTOU window between Stats()'s snapshot (which drops the lock before walking
// the filesystem) and the actual RemoveAll: a stream that started in that gap
// is in s.active by now, and deleting its file would pull the rug out from under
// an in-flight HLS transcode ("torrent closed" → demux I/O error → segment 404).
// Favorites are intentionally not re-checked here — they were already filtered at
// snapshot time and a torrent rarely becomes a favorite within the eviction loop.
func (s *Streamer) evictionBlocked(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Match active torrents by their on-disk name (t.Name()), tolerating the
	// single-file ".part" suffix anacrolix uses while a download is in flight.
	stripped := strings.TrimSuffix(name, ".part")
	for _, e := range s.active {
		if tn := e.t.Name(); tn == name || tn == stripped {
			return true
		}
	}
	if _, ok := s.downloads[name]; ok {
		return true
	}
	if stripped != name {
		if _, ok := s.downloads[stripped]; ok {
			return true
		}
	}
	return false
}

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

type entry struct {
	t          *torrent.Torrent
	lastAccess time.Time
	// Rate sampling: anacrolix only exposes cumulative byte counters, so we cache the
	// previous sample and compute per-second rates from the delta between buildInfo calls.
	lastBytesRead    int64
	lastBytesWritten int64
	lastSampleAt     time.Time
	// paused tracks the soft-pause state. anacrolix has no native Pause; we
	// model it by setting MaxEstablishedConns to 0 so no new peers connect.
	paused bool
	// priority is the user-facing label ("low" | "normal" | "high"). Applied to
	// every file via File.SetPriority on transitions.
	priority string
	// viewers counts open player sessions watching this torrent (a "lease"). A
	// stream-only torrent with no viewers is ephemeral and should stop seeding
	// instead of lingering until the idle reaper. While viewers > 0 the torrent
	// survives — so closing one of several browsers doesn't kill the others.
	viewers int
	// dropTimer schedules the drop a short grace period after the LAST viewer
	// leaves (see ReleaseViewer/viewerGrace). AcquireViewer cancels it, so a
	// quick reopen — or React StrictMode's mount→unmount→mount in dev — doesn't
	// tear the torrent down mid-playback.
	dropTimer *time.Timer
}

// FileInfo is the JSON-friendly view of a file inside a torrent.
type FileInfo struct {
	Index      int     `json:"index"`
	Path       string  `json:"path"`
	Size       int64   `json:"size"`
	IsVideo    bool    `json:"isVideo"`
	Downloaded int64   `json:"downloaded"`
	Progress   float64 `json:"progress"` // 0..1
	Priority   string  `json:"priority"` // none|low|normal|high
}

// TorrentInfo is the JSON-friendly view returned to the frontend.
type TorrentInfo struct {
	InfoHash  string     `json:"infoHash"`
	Name      string     `json:"name"`
	TotalSize int64      `json:"totalSize"`
	Files     []FileInfo `json:"files"`
	Peers     int        `json:"peers"`
	Seeders   int        `json:"seeders"`
	DownRate  int64      `json:"downRate"` // bytes/sec, sampled between polls
	UpRate    int64      `json:"upRate"`   // bytes/sec, sampled between polls
	// Cumulative payload byte counters. BytesDownloaded is the completed bytes of
	// the selected pieces; BytesUploaded is what we've served this SESSION (the
	// anacrolix counter resets when the torrent is re-added — e.g. after a restart).
	BytesDownloaded int64   `json:"bytesDownloaded"`
	BytesUploaded   int64   `json:"bytesUploaded"`
	Progress        float64 `json:"progress"`
	PrimaryFile     int     `json:"primaryFile"` // suggested video file index
	// Status is one of "downloading", "paused", "seeding", "complete".
	// Surfaced for the Transmission-style downloads UI.
	Status string `json:"status,omitempty"`
	// Priority is the user-set piece priority ("low" | "normal" | "high"); empty
	// when the user has not changed it from the anacrolix default.
	Priority string   `json:"priority,omitempty"`
	Trackers []string `json:"trackers,omitempty"`
}

// GlobalRate aggregates download/upload rates across all active torrents.
type GlobalRate struct {
	DownRate       int64 `json:"downRate"`
	UpRate         int64 `json:"upRate"`
	ActiveTorrents int   `json:"activeTorrents"`
}

// PeerInfo is the JSON-friendly view of one connected peer, for the downloads
// "Peers" panel. anacrolix v1.61.0 doesn't export choke/interest, so Sending /
// Receiving are INFERRED from live transfer rates rather than read directly.
type PeerInfo struct {
	Addr         string  `json:"addr"`
	Client       string  `json:"client,omitempty"`
	Network      string  `json:"network,omitempty"` // "tcp" | "utp" | ...
	Availability float64 `json:"availability"`      // 0..1 fraction of pieces the peer has
	DownRate     int64   `json:"downRate"`          // bytes/s we receive from this peer
	UpRate       int64   `json:"upRate"`            // bytes/s we send to this peer
	Downloaded   int64   `json:"downloaded"`        // data bytes read from this peer
	Uploaded     int64   `json:"uploaded"`          // data bytes written to this peer
	IsSeeder     bool    `json:"isSeeder"`          // peer reports all pieces
	Receiving    bool    `json:"receiving"`         // inferred: downRate > 0
	Sending      bool    `json:"sending"`           // inferred: upRate > 0
	Encrypted    bool    `json:"encrypted,omitempty"`
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

func (s *Streamer) metainfoPath(h metainfo.Hash) string {
	return filepath.Join(s.metainfoDir, h.HexString()+".torrent")
}

func (s *Streamer) MetainfoPath(h metainfo.Hash) string {
	return s.metainfoPath(h)
}

const (
	magnetPrefix           = "magnet:"
	errFileIndexOutOfRange = "file index %d out of range"
)

// ParseMagnet validates a magnet URI and extracts its info hash + display
// name without touching the network. Used by the import flow to preview what
// a pasted magnet resolves to before committing it to favorites.
func (s *Streamer) ParseMagnet(magnet string) (hash, name string, err error) {
	if i := strings.Index(magnet, magnetPrefix); i >= 0 {
		magnet = magnet[i:]
	}
	mi, err := metainfo.ParseMagnetUri(magnet)
	if err != nil {
		return "", "", fmt.Errorf("magnet inválido: %w", err)
	}
	name = mi.DisplayName
	if name == "" {
		name = mi.InfoHash.HexString()
	}
	return mi.InfoHash.HexString(), name, nil
}

// ImportTorrentBytes parses a raw .torrent file, persists its metainfo to the
// cache (so a later play skips the DHT round-trip), and returns the info hash
// + torrent name. Does NOT add the torrent to the active set — the import flow
// only records a favorite; playback adds it on demand.
func (s *Streamer) ImportTorrentBytes(data []byte) (hash, name string, err error) {
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return "", "", fmt.Errorf(".torrent inválido: %w", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return "", "", fmt.Errorf("metadados do .torrent ilegíveis: %w", err)
	}
	h := mi.HashInfoBytes()
	// Persist so playback is instant (no DHT). Best-effort.
	if s.metainfoDir != "" {
		path := s.metainfoPath(h)
		if f, ferr := os.CreateTemp(s.metainfoDir, ".tmp-*.torrent"); ferr == nil {
			if werr := mi.Write(f); werr == nil {
				_ = f.Close()
				_ = os.Rename(f.Name(), path)
			} else {
				_ = f.Close()
				_ = os.Remove(f.Name())
			}
		}
	}
	name = info.Name
	if name == "" {
		name = h.HexString()
	}
	return h.HexString(), name, nil
}

// loadCachedMetainfo reads a persisted .torrent for the given hash.
// Returns nil if absent or unreadable — caller falls back to magnet flow.
func (s *Streamer) loadCachedMetainfo(h metainfo.Hash) *metainfo.MetaInfo {
	if s.metainfoDir == "" {
		return nil
	}
	mi, err := metainfo.LoadFromFile(s.metainfoPath(h))
	if err != nil {
		return nil
	}
	return mi
}

// persistMetainfo writes the torrent's metainfo to disk so the next cold
// open skips DHT. Best-effort — errors are logged but don't fail the Add.
func (s *Streamer) persistMetainfo(t *torrent.Torrent) {
	if s.metainfoDir == "" || t == nil {
		return
	}
	mi := t.Metainfo()
	path := s.metainfoPath(t.InfoHash())
	// Write to tmp + rename for atomicity (avoid leaving a half-written
	// file that loadCachedMetainfo would treat as garbage). Disk-full / perms
	// failures were silently swallowed — the .torrent cache never built and
	// future plays fell back to slow DHT with no log to explain why; surface it.
	short := t.InfoHash().HexString()[:8]
	f, err := os.CreateTemp(s.metainfoDir, ".tmp-*.torrent")
	if err != nil {
		log.Printf("streamer: persist metainfo (create temp) failed for %s: %v", short, err)
		return
	}
	if err := mi.Write(f); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		log.Printf("streamer: persist metainfo (write) failed for %s: %v", short, err)
		return
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		log.Printf("streamer: persist metainfo (close) failed for %s: %v", short, err)
		return
	}
	if err := os.Rename(f.Name(), path); err != nil {
		_ = os.Remove(f.Name())
		log.Printf("streamer: persist metainfo (rename) failed for %s: %v", short, err)
	}
}

// maybePersistSeed records the torrent in the seed store when it matches a
// configured seed-tracker, so seeding resumes automatically on the next boot.
// No-op without a seed store or when the torrent isn't a keep-seeding match.
// DropSeed para de auto-seedar um torrent de vez: remove o registro PERSISTENTE
// (.seeds.db) para que ele NÃO volte no próximo boot (resumeSeeding) e o dropa
// da memória. Usar nas ações EXPLÍCITAS do usuário (parar de seedar / remover
// torrent / excluir download) — ao contrário do Drop genérico (idle/health),
// que preserva o auto-seed. Sem isto, um torrent auto-seedado reaparecia como
// "ativo" para sempre, mesmo após ser removido.
func (s *Streamer) DropSeed(hash metainfo.Hash) {
	if s.seeds != nil {
		if err := s.seeds.Remove(hash.HexString()); err != nil {
			log.Printf("streamer: remove persisted seed %s failed: %v", hash.HexString()[:8], err)
		}
	}
	s.Drop(hash)
}

func (s *Streamer) maybePersistSeed(t *torrent.Torrent) {
	if s.seeds == nil || t == nil {
		return
	}
	s.mu.Lock()
	keep := s.shouldKeepSeeding(t)
	s.mu.Unlock()
	if !keep {
		return
	}
	if err := s.seeds.Add(t.InfoHash().HexString(), magnetFromTorrent(t), t.Name()); err != nil {
		log.Printf("streamer: persist seed %s failed: %v", t.InfoHash().HexString()[:8], err)
	}
}

// magnetFromTorrent reconstructs a magnet URI (info_hash + full announce list,
// passkeys included) good enough to re-add the torrent for seeding on boot.
func magnetFromTorrent(t *torrent.Torrent) string {
	m := metainfo.Magnet{InfoHash: t.InfoHash(), DisplayName: t.Name()}
	mi := t.Metainfo()
	for _, tier := range mi.UpvertedAnnounceList() {
		m.Trackers = append(m.Trackers, tier...)
	}
	return m.String()
}

func (s *Streamer) Close() {
	close(s.stop)
	s.client.Close()
	// Fecha o storage mmap (libera mapeamentos/handles). FileStorage default é
	// gerido pelo client, então storageImpl é nil nesse caso.
	if s.storageImpl != nil {
		_ = s.storageImpl.Close()
	}
	if s.dlPieceCompletion != nil {
		_ = s.dlPieceCompletion.Close()
	}
}

// FileReader returns a ReadSeeker for one file, configured for streaming.
// The reader keeps the torrent alive (refreshes lastAccess on each read).
func (s *Streamer) FileReader(hash metainfo.Hash, fileIdx int) (io.ReadSeekCloser, *torrent.File, error) {
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		e.lastAccess = time.Now()
	}
	s.mu.Unlock()
	if !ok {
		return nil, nil, ErrTorrentNotActive
	}

	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return nil, nil, fmt.Errorf("índice de arquivo %d fora do intervalo (0..%d)", fileIdx, len(files)-1)
	}
	f := files[fileIdx]

	r := f.NewReader()
	// Readahead sized for the HLS transcode path. ffmpeg reads the source
	// sequentially and each 4s HLS segment of 4K video pulls ~15 MB; with only
	// 8 MiB of readahead the anacrolix Reader blocks waiting for the next piece
	// mid-segment, and WaitForMaster times out before the first segment lands
	// (confirmed on the GTX 1070 with 2160p sources). 32 MiB covers ~2 segments
	// of 4K lookahead so the encoder never starves on a healthy swarm. Configurável
	// via StreamConfig.ReadaheadMB (default 32) — ver streamReadahead().
	r.SetReadahead(s.streamReadahead())
	r.SetResponsive() // prioritize pieces around current read position

	// Reconcile THIS file's cache against the disk, once. anacrolix assumes an
	// empty store on add and would re-download pieces we already have (seen in
	// prod: 1.16 GB on disk, 0 reported). Scoped to the single file (not the
	// whole torrent) so a season pack doesn't trigger a multi-GB hash storm
	// that starves the encoder. Runs before warmTail so verified head pieces
	// are ready when ffmpeg starts reading.
	go s.verifyFilePieces(hash, fileIdx, f)

	// Warm the TAIL of the file in the background. Container indexes live at the
	// end: MP4 `moov` (non-faststart) and Matroska `Cues` both sit near EOF.
	// ffmpeg seeks there during demux init; if those pieces aren't downloaded,
	// the read blocks ~10s+ AND (since reads are serialized) head-of-lines the
	// sequential probe read, so the first segment never lands inside the wait
	// window. Kicking off the tail pieces NOW — on a separate reader, concurrent
	// with the head — means they're already arriving when ffmpeg asks.
	go s.warmTail(f)

	return &trackingReader{Reader: r, streamer: s, hash: hash}, f, nil
}

// Prefetch hints the anacrolix piece scheduler to start downloading the head of
// `fileIdx` on the already-active torrent, *without* serving any bytes back.
//
// Use case: while the user watches episode N of a series, we kick off pieces of
// N+1 in the background so the cut between episodes is near-instantaneous.
// Same idea for the next item in a playlist when it's the same torrent.
//
// Implementation: opens a Reader, seeks to 0, sets a generous readahead, reads
// a small head chunk, then closes after a short delay so the priority hint
// outlives the request lifecycle. The bytes already on disk stay there — only
// the in-memory priority hint goes away when the reader closes.
//
// Returns immediately; the actual download is asynchronous in anacrolix.
func (s *Streamer) Prefetch(hash metainfo.Hash, fileIdx int) error {
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		e.lastAccess = time.Now()
	}
	s.mu.Unlock()
	if !ok {
		return errors.New("torrent não ativo — chamar /stream/add primeiro")
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return fmt.Errorf("file index %d fora do intervalo (0..%d)", fileIdx, len(files)-1)
	}
	f := files[fileIdx]
	r := f.NewReader()
	r.SetReadahead(8 << 20) // 8 MiB — enough to cover the first few seconds
	r.SetResponsive()
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		// #nosec G104 -- Close best-effort no cleanup; erro no teardown irrelevante
		r.Close()
		return fmt.Errorf("prefetch seek: %w", err)
	}
	// Tiny read just to commit the readahead hint and trigger piece priority.
	go func() {
		buf := make([]byte, 256<<10) // 256 KiB
		// Best-effort read with a soft deadline: even if it blocks waiting for
		// peers, the readahead is already registered so closing later still
		// leaves the pieces queued in anacrolix.
		done := make(chan struct{})
		go func() {
			_, _ = r.Read(buf)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		// #nosec G104 -- Close best-effort no cleanup; erro no teardown irrelevante
		r.Close()
	}()
	return nil
}

// Drop forcibly removes a torrent (stops download, keeps files until GC).
// activeReadGuard: a torrent read within this window is treated as still being
// watched, so an explicit Drop() (player close) is skipped. trackingReader bumps
// lastAccess on every read, including the HLS transcode's source reads.
const activeReadGuard = 60 * time.Second

func (s *Streamer) Drop(hash metainfo.Hash) {
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		// Do not drop while a player still holds a viewer lease — the lease is
		// the authoritative "someone is watching" signal. A forced Drop (manual
		// StreamDrop, health probe) must not kill a co-watcher's playback.
		if e.viewers > 0 {
			s.mu.Unlock()
			return
		}
		// Do not drop if it is registered as an active background download
		if _, protected := s.downloads[e.t.Name()]; protected {
			s.mu.Unlock()
			return
		}
		// Do not drop a torrent another reader is actively streaming. The player
		// calls Drop() on close, but with MULTIPLE sessions on the same torrent
		// (e.g. two browsers, or an HLS transcode still pulling segments for
		// another viewer), an eager drop killed the survivors' ffmpeg mid-playback
		// ("torrent closed" → demux I/O error → segment 404). A recent read means
		// someone is still watching — leave eviction to the idle reaper.
		if time.Since(e.lastAccess) < activeReadGuard {
			s.mu.Unlock()
			return
		}
		delete(s.active, hash)
	}
	s.mu.Unlock()
	if ok {
		e.t.Drop()
		s.purgeVerifiedFiles(hash)
	}
}

// viewerGrace is how long a stream-only torrent lingers after its last viewer
// leaves before being dropped. Short enough to stop seeding promptly, long
// enough to absorb a quick reopen and React StrictMode's dev double-mount.
const viewerGrace = 8 * time.Second

// AcquireViewer registers an open player session ("lease") on a torrent and
// cancels any pending drop. Called when the player opens a stream. No-op if the
// torrent isn't active (e.g. a local file, which lives outside the streamer).
func (s *Streamer) AcquireViewer(hash metainfo.Hash) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.active[hash]
	if !ok {
		return
	}
	e.viewers++
	e.lastAccess = time.Now()
	if e.dropTimer != nil {
		e.dropTimer.Stop()
		e.dropTimer = nil
	}
}

// ReleaseViewer drops a player session's lease. When the LAST viewer leaves a
// stream-only (non-download) torrent, it schedules a drop after viewerGrace
// instead of dropping eagerly — so other viewers keep streaming and a quick
// reopen cancels the teardown.
//
// Returns (scheduled, lastViewer). scheduled is true when a drop was scheduled.
// lastViewer is true whenever THIS call removed the final viewer — even when the
// torrent is kept alive (background download or seed-tracker): the HLS transcode
// exists only to feed the player, so the caller must stop it once nobody is
// watching, while the torrent keeps seeding/downloading on its own.
func (s *Streamer) ReleaseViewer(hash metainfo.Hash) (scheduled, lastViewer bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.active[hash]
	if !ok {
		return false, false
	}
	if e.viewers > 0 {
		e.viewers--
	}
	if e.viewers > 0 {
		return false, false // another viewer still watching
	}
	// No viewers left — the caller stops the HLS transcode regardless of what
	// keeps the torrent alive below.
	// Deliberate background downloads stay alive regardless of viewers.
	if _, protected := s.downloads[e.t.Name()]; protected {
		return false, true
	}
	// Seed-tracker torrents keep uploading after the viewer leaves — never drop.
	if s.shouldKeepSeeding(e.t) {
		return false, true
	}
	if e.dropTimer != nil {
		e.dropTimer.Stop()
	}
	e.dropTimer = time.AfterFunc(viewerGrace, func() { s.dropIfStillIdle(hash, e) })
	return true, true
}

// dropIfStillIdle runs when a viewer-lease grace timer fires. It drops the
// torrent only if nothing changed in the meantime: same entry still active, no
// viewers re-acquired, and not a protected download.
func (s *Streamer) dropIfStillIdle(hash metainfo.Hash, e *entry) {
	s.mu.Lock()
	cur, ok := s.active[hash]
	if !ok || cur != e || e.viewers > 0 {
		s.mu.Unlock()
		return
	}
	if _, protected := s.downloads[e.t.Name()]; protected {
		s.mu.Unlock()
		return
	}
	if s.shouldKeepSeeding(e.t) {
		e.dropTimer = nil
		s.mu.Unlock()
		return
	}
	delete(s.active, hash)
	e.dropTimer = nil
	s.mu.Unlock()
	log.Printf("streamer: dropping stream-only torrent %s (%s) — no viewers", e.t.Name(), hash.HexString()[:8])
	e.t.Drop()
	s.purgeVerifiedFiles(hash)
}

// purgeVerifiedFiles drops the hash-check dedup keys for a torrent when it
// leaves active memory. This per-lifecycle cleanup replaced a blunt
// wipe-the-whole-map-at-2000-entries, which could clear keys for files being
// actively read by another stream and force a needless full re-hash.
func (s *Streamer) purgeVerifiedFiles(hash metainfo.Hash) {
	prefix := hash.HexString() + "-"
	s.verifiedMu.Lock()
	for k := range s.verifiedFiles {
		if strings.HasPrefix(k, prefix) {
			delete(s.verifiedFiles, k)
		}
	}
	s.verifiedMu.Unlock()
}

// ─── internal helpers ────────────────────────────────────────────────────────

func (s *Streamer) buildInfo(e *entry) *TorrentInfo {
	t := e.t
	// Rate sample requires mutating entry counters — must hold s.mu so concurrent
	// GlobalStats / Get callers see a consistent snapshot.
	s.mu.Lock()
	dn, up := sampleRateLocked(e, time.Now())
	s.mu.Unlock()
	st := t.Stats()
	info := &TorrentInfo{
		InfoHash:        t.InfoHash().HexString(),
		Name:            t.Name(),
		TotalSize:       t.Length(),
		Peers:           st.TotalPeers,
		Seeders:         st.ConnectedSeeders,
		DownRate:        dn,
		UpRate:          up,
		BytesDownloaded: t.BytesCompleted(),
		BytesUploaded:   st.BytesWrittenData.Int64(),
	}

	if t.Length() > 0 {
		info.Progress = float64(t.BytesCompleted()) / float64(t.Length())
	}

	// Populate announce trackers list
	mi := t.Metainfo()
	for _, tier := range mi.UpvertedAnnounceList() {
		info.Trackers = append(info.Trackers, tier...)
	}

	files := t.Files()
	info.Files = make([]FileInfo, 0, len(files))
	for i, f := range files {
		ext := strings.ToLower(filepath.Ext(f.Path()))
		isVideo := videoExtensions[ext]
		fi := FileInfo{
			Index:      i,
			Path:       f.Path(),
			Size:       f.Length(),
			IsVideo:    isVideo,
			Downloaded: f.BytesCompleted(),
			Priority:   labelFromPriority(f.Priority()),
		}
		if f.Length() > 0 {
			fi.Progress = float64(f.BytesCompleted()) / float64(f.Length())
		}
		info.Files = append(info.Files, fi)
	}
	info.PrimaryFile = pickPrimaryFile(info.Files)
	return info
}

// seriesEpisodeRe matches the standard "S01E03" or "s1e3" tag inside a path.

// gcLoop runs every minute and drops torrents idle longer than IdleTimeout.
func (s *Streamer) gcLoop() {
	tick := time.NewTicker(1 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-tick.C:
			var dropped []metainfo.Hash
			s.mu.Lock()
			for h, e := range s.active {
				if now.Sub(e.lastAccess) > s.cfg.IdleTimeout {
					// Active downloads stay alive even when idle — the user
					// is waiting for the file to finish in background.
					if _, protected := s.downloads[e.t.Name()]; protected {
						continue
					}
					// Seed-tracker torrents keep seeding regardless of idleness.
					if s.shouldKeepSeeding(e.t) {
						continue
					}
					log.Printf("streamer: dropping idle torrent %s (%s)", e.t.Name(), h.HexString()[:8])
					delete(s.active, h)
					e.t.Drop()
					dropped = append(dropped, h)
				}
			}
			s.mu.Unlock()
			// Purge hash-check dedup keys outside s.mu (purgeVerifiedFiles takes
			// verifiedMu — avoid nesting the two locks).
			for _, h := range dropped {
				s.purgeVerifiedFiles(h)
			}
			// Then enforce cache size cap (LRU over inactive entries)
			s.enforceCacheLimit()
		}
	}
}

// firstChars returns up to n characters from s — for error messages without leaking huge URLs.
func firstChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ─── Active torrent controls (Transmission-style) ───────────────────────────

// NewForTesting returns a Streamer with only the fields the
// non-torrent-client-touching handlers exercise (active map, downloads
// protection set, rate limiters). Opening a real anacrolix client requires
// binding UDP :42069, which collides between parallel test packages and a
// running dev server. Use this in handler/unit tests that don't need the
// torrent transport.
func NewForTesting() *Streamer {
	return &Streamer{
		active:        make(map[metainfo.Hash]*entry),
		downloads:     make(map[string]struct{}),
		dlLimiter:     rate.NewLimiter(rate.Inf, 1<<16),
		upLimiter:     rate.NewLimiter(rate.Inf, 1<<16),
		verifiedFiles: make(map[string]bool),
	}
}

// trackingReader wraps a torrent.Reader so each read refreshes lastAccess.
type trackingReader struct {
	torrent.Reader
	streamer *Streamer
	hash     metainfo.Hash
}

func (r *trackingReader) Read(p []byte) (int, error) {
	r.streamer.mu.Lock()
	if e, ok := r.streamer.active[r.hash]; ok {
		e.lastAccess = time.Now()
	}
	r.streamer.mu.Unlock()
	return r.Reader.Read(p)
}
