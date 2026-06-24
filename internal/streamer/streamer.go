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
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	alog "github.com/anacrolix/log"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/anacrolix/torrent/types"
	"github.com/lgldsilva/jackui/internal/diskutil"
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
	// ListenPort is the BitTorrent peer port for inbound connections. 0 → 51469.
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
	// leaves — so private-tracker content (e.g. amigos-share) keeps uploading
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

// SetFilePathResolver registers the custom file path resolver function (typically querying the downloads DB).
func (s *Streamer) SetFilePathResolver(r FilePathResolver) {
	s.filePathResolver = r
}

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
	InfoHash    string     `json:"infoHash"`
	Name        string     `json:"name"`
	TotalSize   int64      `json:"totalSize"`
	Files       []FileInfo `json:"files"`
	Peers       int        `json:"peers"`
	Seeders     int        `json:"seeders"`
	DownRate    int64      `json:"downRate"` // bytes/sec, sampled between polls
	UpRate      int64      `json:"upRate"`   // bytes/sec, sampled between polls
	Progress    float64    `json:"progress"`
	PrimaryFile int        `json:"primaryFile"` // suggested video file index
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
		cfg.ListenPort = 51469
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
	_ = os.MkdirAll(metainfoDir, 0o755)

	s := &Streamer{
		cfg:           cfg,
		client:        client,
		active:        make(map[metainfo.Hash]*entry),
		stop:          make(chan struct{}),
		downloads:     make(map[string]struct{}),
		metainfoDir:   metainfoDir,
		verifiedFiles: make(map[string]bool),
		dlLimiter:     dlLimiter,
		upLimiter:     upLimiter,
		storageImpl:   storageImpl,
		readahead:     cfg.Readahead,
		seedTrackers:  normalizeSeedTrackers(cfg.SeedTrackers),
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
	errTorrentNotActive    = "torrent não está ativo"
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
}

// Add loads a magnet OR an HTTP(S) URL to a .torrent file and waits for metadata.
// Returns the torrent info once available.
//
// For .torrent URLs (common in private trackers and some Jackett providers that
// don't return a magnet), we fetch the file, parse the metainfo, and add via
// AddTorrentSpec — same downstream behavior as magnet.
func (s *Streamer) Add(ctx context.Context, magnetOrURL string) (*TorrentInfo, error) {
	src := cleanSource(magnetOrURL)
	t, err := s.resolveSource(ctx, src)
	if err != nil {
		return nil, err
	}
	t.AddTrackers(publicTrackers)
	if err := waitForMetadata(ctx, t, s.cfg.MetadataWait); err != nil {
		return nil, err
	}
	return s.registerTorrent(t), nil
}

func cleanSource(magnetOrURL string) string {
	src := strings.TrimSpace(magnetOrURL)
	src = strings.TrimPrefix(src, "\xef\xbb\xbf")
	return src
}

func (s *Streamer) resolveSource(ctx context.Context, src string) (*torrent.Torrent, error) {
	lower := strings.ToLower(src[:min(16, len(src))])
	switch {
	case isMagnet(lower, src):
		return s.resolveMagnet(src)
	case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://"):
		return s.addFromTorrentURL(ctx, src)
	default:
		return nil, fmt.Errorf("unsupported source — provide a magnet: or http(s):// URL (got %q)", firstChars(src, 30))
	}
}

func isMagnet(lower, src string) bool {
	if strings.HasPrefix(lower, magnetPrefix) || strings.Contains(lower, magnetPrefix) {
		return true
	}
	return false
}

func (s *Streamer) resolveMagnet(src string) (*torrent.Torrent, error) {
	if i := strings.Index(src, magnetPrefix); i >= 0 {
		src = src[i:]
	}
	if mi, err := metainfo.ParseMagnetUri(src); err == nil {
		if cached := s.loadCachedMetainfo(mi.InfoHash); cached != nil {
			t, err := s.client.AddTorrent(cached)
			if err != nil {
				return nil, fmt.Errorf("add cached metainfo: %w", err)
			}
			return t, nil
		}
	}
	t, err := s.client.AddMagnet(src)
	if err != nil {
		return nil, fmt.Errorf("add magnet: %w", err)
	}
	return t, nil
}

func waitForMetadata(ctx context.Context, t *torrent.Torrent, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-t.GotInfo():
		return nil
	case <-waitCtx.Done():
		return fmt.Errorf("timeout aguardando metadados do torrent (%s)", timeout)
	}
}

func (s *Streamer) registerTorrent(t *torrent.Torrent) *TorrentInfo {
	now := time.Now()
	s.mu.Lock()
	e, ok := s.active[t.InfoHash()]
	if ok {
		// Already active — REUSE the entry. A re-Add of an active torrent
		// (prefetching another file of the SAME torrent, a health probe, VLC
		// resolving info) must NOT discard the live viewer lease / pause /
		// priority state. Overwriting it reset viewers→0, so the next
		// ReleaseViewer scheduled a drop while playback was still going.
		e.t = t
		e.lastAccess = now
	} else {
		e = &entry{t: t, lastAccess: now, lastSampleAt: now}
		s.active[t.InfoHash()] = e
	}
	s.mu.Unlock()
	s.persistMetainfo(t)
	s.maybePersistSeed(t)
	info := s.buildInfo(e)
	if s.cache != nil {
		_ = s.cache.Set(info)
	}
	return info
}

// addFromTorrentURL handles a HTTP(S) URL that may either:
//   - Serve a .torrent file directly (binary bencoded body)
//   - Respond with a 301/302 redirect pointing to a magnet: URI
//
// The second case is what Jackett does for many providers (e.g., torrentdownload):
// the `/dl/...` endpoint redirects to `magnet:?xt=urn:btih:...`. The default Go
// http.Client follows the redirect and chokes on the magnet scheme.
//
// We detect the magnet redirect via CheckRedirect, capture the magnet URL, and
// add via AddMagnet instead of trying to fetch.
// isBlockedFetchIP reports whether an IP is off-limits for server-side fetches
// (SSRF protection): loopback, private RFC1918/ULA, link-local, and the
// unspecified address. .torrent URLs from indexers are public, so blocking
// these doesn't hurt legitimate use but stops a caller from making the server
// probe the internal homelab network or metadata endpoints.
func isBlockedFetchIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

func newSSRFGuardedClient(jackettHost string, capturedMagnet *string) *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: newSSRFTransport(jackettHost),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return checkRedirect(req, via, capturedMagnet)
		},
	}
}

func newSSRFTransport(jackettHost string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return ssrfDialContext(ctx, network, addr, jackettHost)
		},
	}
}

func ssrfDialContext(ctx context.Context, network, addr, jackettHost string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	trusted := jackettHost != "" && host == jackettHost
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if !trusted {
		for _, ip := range ips {
			if isBlockedFetchIP(ip.IP) {
				return nil, fmt.Errorf("refusing to fetch from non-public address %s", ip.IP)
			}
		}
	}
	d := net.Dialer{}
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

func checkRedirect(req *http.Request, via []*http.Request, capturedMagnet *string) error {
	if strings.HasPrefix(strings.ToLower(req.URL.String()), magnetPrefix) {
		*capturedMagnet = req.URL.String()
		return http.ErrUseLastResponse
	}
	if len(via) >= 10 {
		return fmt.Errorf("too many redirects")
	}
	return nil
}

func (s *Streamer) injectJackettAPIKey(torrentURL string) string {
	if s.cfg.JackettHost == "" {
		return torrentURL
	}
	u, err := url.Parse(torrentURL)
	if err != nil || u.Hostname() != s.cfg.JackettHost {
		return torrentURL
	}
	if s.cfg.JackettAPIKey == "" || u.Query().Get("apikey") != "" {
		return torrentURL
	}
	q := u.Query()
	q.Set("apikey", s.cfg.JackettAPIKey)
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Streamer) addFromCapturedMagnet(magnet string) (*torrent.Torrent, error) {
	t, err := s.client.AddMagnet(magnet)
	if err != nil {
		return nil, fmt.Errorf("add magnet from redirect: %w", err)
	}
	return t, nil
}

func (s *Streamer) addFromTorrentResponse(resp *http.Response) (*torrent.Torrent, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(".torrent URL returned %d", resp.StatusCode)
	}
	mi, err := metainfo.Load(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("parse .torrent: %w", err)
	}
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		return nil, fmt.Errorf("metainfo spec: %w", err)
	}
	t, _, err := s.client.AddTorrentSpec(spec)
	return t, err
}

func (s *Streamer) addFromTorrentURL(ctx context.Context, torrentURL string) (*torrent.Torrent, error) {
	var capturedMagnet string

	torrentURL = s.injectJackettAPIKey(torrentURL)
	httpClient := newSSRFGuardedClient(s.cfg.JackettHost, &capturedMagnet)

	req, err := http.NewRequestWithContext(ctx, "GET", torrentURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch torrent URL: %w", err)
	}
	defer resp.Body.Close()

	if capturedMagnet != "" {
		return s.addFromCapturedMagnet(capturedMagnet)
	}
	return s.addFromTorrentResponse(resp)
}

// Get returns the current TorrentInfo for an active torrent.
func (s *Streamer) Get(hash metainfo.Hash) (*TorrentInfo, error) {
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		e.lastAccess = time.Now()
	}
	s.mu.Unlock()
	if !ok {
		return nil, errors.New("torrent não encontrado (expirou ou nunca foi adicionado)")
	}
	return s.buildInfo(e), nil
}

// Peers returns a snapshot of the currently-connected peers of an active
// torrent for the downloads inspector. Errors when the torrent isn't active
// (dropped or never opened). The peer set is read live from anacrolix.
func (s *Streamer) Peers(hash metainfo.Hash) ([]PeerInfo, error) {
	s.mu.Lock()
	e, ok := s.active[hash]
	s.mu.Unlock()
	if !ok {
		return nil, errors.New("torrent não encontrado (expirou ou nunca foi adicionado)")
	}
	t := e.t
	numPieces := t.NumPieces()
	conns := t.PeerConns()
	out := make([]PeerInfo, 0, len(conns))
	for _, pc := range conns {
		st := pc.Stats()
		pi := PeerInfo{
			Network:    pc.Network,
			DownRate:   int64(pc.DownloadRate()),
			UpRate:     int64(st.LastWriteUploadRate),
			Downloaded: st.BytesReadData.Int64(),
			Uploaded:   st.BytesWrittenData.Int64(),
			Encrypted:  pc.PeerPrefersEncryption,
		}
		if pc.RemoteAddr != nil {
			pi.Addr = pc.RemoteAddr.String()
		}
		if name, _ := pc.PeerClientName.Load().(string); name != "" {
			pi.Client = name
		}
		if numPieces > 0 {
			pi.Availability = float64(st.RemotePieceCount) / float64(numPieces)
			pi.IsSeeder = st.RemotePieceCount >= numPieces
		}
		pi.Receiving = pi.DownRate > 0
		pi.Sending = pi.UpRate > 0
		out = append(out, pi)
	}
	return out, nil
}

// GlobalStats returns aggregate download/upload rates across all active torrents.
// Snapshot taken under the streamer lock; safe to poll from a handler.
func (s *Streamer) GlobalStats() GlobalRate {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := GlobalRate{ActiveTorrents: len(s.active)}
	now := time.Now()
	for _, e := range s.active {
		dn, up := sampleRateLocked(e, now)
		g.DownRate += dn
		g.UpRate += up
	}
	return g
}

// sampleRateLocked computes per-second rates for an entry by diffing against the
// previous sample and then updates the entry's sample state. Caller holds s.mu.
//
// Returns (0, 0) on the very first sample after Add or when the elapsed window is
// too small (< 250ms) to give a meaningful rate.
func sampleRateLocked(e *entry, now time.Time) (downRate, upRate int64) {
	st := e.t.Stats()
	br := st.BytesReadData.Int64()
	bw := st.BytesWrittenData.Int64()
	elapsed := now.Sub(e.lastSampleAt).Seconds()

	if e.lastSampleAt.IsZero() || elapsed < 0.25 {
		// First sample after Add or sampled too soon — record and emit zero.
		e.lastBytesRead = br
		e.lastBytesWritten = bw
		e.lastSampleAt = now
		return 0, 0
	}

	dr := br - e.lastBytesRead
	dw := bw - e.lastBytesWritten
	if dr < 0 {
		dr = 0 // counter reset (e.g., torrent dropped + re-added)
	}
	if dw < 0 {
		dw = 0
	}
	downRate = int64(float64(dr) / elapsed)
	upRate = int64(float64(dw) / elapsed)

	e.lastBytesRead = br
	e.lastBytesWritten = bw
	e.lastSampleAt = now
	return downRate, upRate
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
		return nil, nil, errors.New(errTorrentNotActive)
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

// VerifyFile is the exported entrypoint para o worker de downloads disparar a
// reconciliação de pieces no disco antes de pedir mais dados ao swarm. Reusa
// o mesmo dedupe set (`verifiedFiles`) que o caminho de streaming, então a
// verificação acontece NO MÁXIMO uma vez por (hash, file) por processo —
// não importa se foi streaming ou download que disparou primeiro.
//
// Background: anacrolix tradicionalmente não re-verifica em startup; confia no
// bolt DB. Se o shutdown anterior foi ungraceful (SIGKILL, container OOM), o
// bolt fica desatualizado e anacrolix "esquece" pieces que estão no disco.
// Sem essa chamada, o worker pede ao swarm bytes que já temos.
func (s *Streamer) VerifyFile(hash metainfo.Hash, fileIdx int) error {
	s.mu.Lock()
	e, ok := s.active[hash]
	s.mu.Unlock()
	if !ok {
		return errors.New(ErrTorrentNotActive)
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return fmt.Errorf(errFileIndexOutOfRange, fileIdx)
	}
	s.verifyFilePieces(hash, fileIdx, files[fileIdx])
	return nil
}

// VerifyTorrent reconciles on-disk pieces for EVERY file of a torrent — the
// whole-torrent download path. Same rationale and per-(hash,file) dedupe as
// VerifyFile, applied file by file (sequencial: custo proporcional ao que está
// no disco; pieces ausentes falham o hash rápido via sparse reads).
func (s *Streamer) VerifyTorrent(hash metainfo.Hash) error {
	s.mu.Lock()
	e, ok := s.active[hash]
	s.mu.Unlock()
	if !ok {
		return errors.New(ErrTorrentNotActive)
	}
	for i, f := range e.t.Files() {
		s.verifyFilePieces(hash, i, f)
	}
	return nil
}

// RecheckAllFiles força o "Force Recheck" em TODOS os arquivos de um torrent
// (download de torrent inteiro). Mesmo contrato do RecheckFile; os arquivos são
// re-hashados sequencialmente numa única goroutine — um torrent de milhares de
// arquivos não pode disparar milhares de hash loops concorrentes.
func (s *Streamer) RecheckAllFiles(hash metainfo.Hash) error {
	s.mu.Lock()
	e, ok := s.active[hash]
	s.mu.Unlock()
	if !ok {
		return errors.New(ErrTorrentNotActive)
	}
	files := e.t.Files()
	go func() {
		for i, f := range files {
			key := fmt.Sprintf("%s-%d", hash.HexString(), i)
			s.verifiedMu.Lock()
			delete(s.verifiedFiles, key)
			s.verifiedMu.Unlock()
			s.asyncRecheckFile(key, f)
		}
	}()
	return nil
}

// RecheckFile força uma re-verificação completa dos pieces de um arquivo,
// IGNORANDO o dedup do verifiedFiles e re-hashando até pieces marcados como
// "complete" no momento. Caso de uso: ação manual do user via UI ("recheck")
// quando ele suspeita que os bytes no disco estão corrompidos (BitErrors)
// ou quando o tamanho/contagem do downloads.db não bate com o real.
// Diferente do VerifyFile, que pula pieces já completos e dedupa por processo,
// aqui valida tudo de novo — semantics equivalente ao "Force Recheck" do
// qBittorrent. Roda em goroutine porque um filme grande leva minutos.
func (s *Streamer) RecheckFile(hash metainfo.Hash, fileIdx int) error {
	s.mu.Lock()
	e, ok := s.active[hash]
	s.mu.Unlock()
	if !ok {
		return errors.New(ErrTorrentNotActive)
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return fmt.Errorf(errFileIndexOutOfRange, fileIdx)
	}
	// Libera o claim do dedup antes de re-hashar — assim a verificação roda
	// de verdade. Mantém a guarda: se outro recheck já está em voo no mesmo
	// (hash,fileIdx), LoadOrStore retorna loaded=true e a 2ª chamada vira no-op.
	key := fmt.Sprintf("%s-%d", hash.HexString(), fileIdx)
	s.verifiedMu.Lock()
	delete(s.verifiedFiles, key)
	s.verifiedMu.Unlock()
	f := files[fileIdx]
	go s.asyncRecheckFile(key, f)
	return nil
}

// asyncRecheckFile handles the asynchronous re-hashing of a file's pieces.
func (s *Streamer) asyncRecheckFile(key string, f *torrent.File) {
	// Marca como em-progresso antes da hashagem pra concorrent calls não
	// dispararem 2ª pass.
	s.verifiedMu.Lock()
	if s.verifiedFiles == nil {
		s.verifiedFiles = make(map[string]bool)
	}
	_, loaded := s.verifiedFiles[key]
	if !loaded {
		// No blunt wipe-at-2000 here: keys are purged per-torrent on Drop
		// (purgeVerifiedFiles), so this map tracks only currently-active torrents.
		s.verifiedFiles[key] = true
	}
	s.verifiedMu.Unlock()
	if loaded {
		return
	}

	completed := false
	defer func() {
		if !completed {
			s.verifiedMu.Lock()
			delete(s.verifiedFiles, key)
			s.verifiedMu.Unlock()
		}
	}()
	for p := range f.Pieces() {
		_ = p.VerifyData() // todos os pieces, sem o skip-complete do VerifyFile
	}
	completed = true
}

// verifyFilePieces hash-checks the on-disk pieces backing a single file so the
// scheduler reuses the cache instead of re-downloading. Runs once per
// (hash,fileIdx) per process. Verifying only this file's piece range keeps the
// cost proportional to what's being watched, not the whole (possibly huge)
// torrent. Pieces missing from disk fail their hash quickly (sparse reads).
func (s *Streamer) verifyFilePieces(hash metainfo.Hash, fileIdx int, f *torrent.File) {
	key := fmt.Sprintf("%s-%d", hash.HexString(), fileIdx)
	// Claim the file so two concurrent readers don't both hash it.
	s.verifiedMu.Lock()
	if s.verifiedFiles == nil {
		s.verifiedFiles = make(map[string]bool)
	}
	_, loaded := s.verifiedFiles[key]
	if !loaded {
		// No blunt wipe-at-2000 here: keys are purged per-torrent on Drop
		// (purgeVerifiedFiles), so this map tracks only currently-active torrents.
		s.verifiedFiles[key] = true
	}
	s.verifiedMu.Unlock()
	if loaded {
		return // already reconciled (or in progress) for this file
	}
	// If we bail before finishing (panic, or the torrent gets dropped mid-loop),
	// drop the claim so a later read can retry. Marking "verified" up front and
	// never clearing it meant an interrupted pass disabled reconciliation for
	// the whole process lifetime → re-downloading pieces already on disk.
	completed := false
	defer func() {
		if !completed {
			s.verifiedMu.Lock()
			delete(s.verifiedFiles, key)
			s.verifiedMu.Unlock()
		}
	}()
	for p := range f.Pieces() {
		// Only verify pieces that have bytes on disk; fully-missing pieces have
		// nothing to reconcile and verifying them just wastes a hash pass.
		if p.State().Complete {
			continue
		}
		_ = p.VerifyData()
	}
	completed = true
}

// warmTail prioritizes the last few MB of a file so the container index
// (moov/Cues) is downloading before ffmpeg seeks to it. Best-effort, bounded:
// opens its own reader (independent cursor, no contention with the main read),
// reads a small tail window, then closes after a short grace period.
func (s *Streamer) warmTail(f *torrent.File) {
	const tail = 8 << 20 // 8 MiB from the end
	length := f.Length()
	if length <= tail {
		return // small file — head readahead already covers it
	}
	r := f.NewReader()
	r.SetReadahead(tail)
	r.SetResponsive()
	if _, err := r.Seek(length-tail, io.SeekStart); err != nil {
		r.Close()
		return
	}
	buf := make([]byte, 256<<10)
	done := make(chan struct{})
	go func() {
		_, _ = r.Read(buf) // commit the priority hint; bytes themselves discarded
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
	}
	r.Close()
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
// reopen cancels the teardown. Returns true when a drop was scheduled, so the
// caller can tear down the HLS session for the same hash.
func (s *Streamer) ReleaseViewer(hash metainfo.Hash) (scheduled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.active[hash]
	if !ok {
		return false
	}
	if e.viewers > 0 {
		e.viewers--
	}
	if e.viewers > 0 {
		return false // another viewer still watching
	}
	// Deliberate background downloads stay alive regardless of viewers.
	if _, protected := s.downloads[e.t.Name()]; protected {
		return false
	}
	// Seed-tracker torrents keep uploading after the viewer leaves — never drop.
	if s.shouldKeepSeeding(e.t) {
		return false
	}
	if e.dropTimer != nil {
		e.dropTimer.Stop()
	}
	e.dropTimer = time.AfterFunc(viewerGrace, func() { s.dropIfStillIdle(hash, e) })
	return true
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
	info := &TorrentInfo{
		InfoHash:  t.InfoHash().HexString(),
		Name:      t.Name(),
		TotalSize: t.Length(),
		Peers:     t.Stats().TotalPeers,
		Seeders:   t.Stats().ConnectedSeeders,
		DownRate:  dn,
		UpRate:    up,
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
var seriesEpisodeRe = regexp.MustCompile(`(?i)s(\d{1,2})e(\d{1,3})`)

// extraTagsRe matches things that look like Featurettes / Extras / Sample / Trailer
// — files we should NEVER pick as primary on a series torrent.
var extraTagsRe = regexp.MustCompile(`(?i)\b(featurette|extras?|bonus|behind[\s\-]?the[\s\-]?scenes|deleted[\s\-]?scenes|making[\s\-]?of|sample|trailer|interview|gag[\s\-]?reel|outtake)s?\b`)

// pickPrimaryFile chooses the file to auto-select when the user "Plays" a
// torrent without specifying one. Picks "the most likely main content":
//
//  1. If 3+ files contain an S?E? pattern AND aren't tagged as extras, pick
//     the lowest (season, episode) episode — the natural starting point for
//     a series pack. This handles Breaking Bad-style torrents that ship with
//     huge Featurettes that would dwarf a real episode by size.
//  2. Else pick the largest video that isn't tagged as an extra — covers
//     single-movie torrents and series with non-standard naming.
//  3. Fall back to the first video, or -1 if none.
func pickPrimaryFile(files []FileInfo) int {
	if idx, ok := pickEpisodeStart(files); ok {
		return idx
	}
	if idx, ok := pickLargestNonExtra(files); ok {
		return idx
	}
	return firstVideoIndex(files)
}

func nonExtraVideos(files []FileInfo) []FileInfo {
	var out []FileInfo
	for _, f := range files {
		if f.IsVideo && !extraTagsRe.MatchString(f.Path) {
			out = append(out, f)
		}
	}
	return out
}

func pickEpisodeStart(files []FileInfo) (int, bool) {
	type epHit struct{ idx, season, episode int }
	var episodes []epHit
	for _, f := range nonExtraVideos(files) {
		m := seriesEpisodeRe.FindStringSubmatch(f.Path)
		if m == nil {
			continue
		}
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		episodes = append(episodes, epHit{idx: f.Index, season: s, episode: e})
	}
	if len(episodes) < 3 {
		return 0, false
	}
	best := episodes[0]
	for _, ep := range episodes[1:] {
		if ep.season < best.season || (ep.season == best.season && ep.episode < best.episode) {
			best = ep
		}
	}
	return best.idx, true
}

func pickLargestNonExtra(files []FileInfo) (int, bool) {
	largestIdx, largestSize := -1, int64(0)
	for _, f := range nonExtraVideos(files) {
		if f.Size > largestSize {
			largestIdx, largestSize = f.Index, f.Size
		}
	}
	if largestIdx >= 0 {
		return largestIdx, true
	}
	return 0, false
}

func firstVideoIndex(files []FileInfo) int {
	for _, f := range files {
		if f.IsVideo {
			return f.Index
		}
	}
	return -1
}

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

// ─── Cache management ───────────────────────────────────────────────────────

// CacheEntry describes one item on disk in the cache directory.
type CacheEntry struct {
	Path       string    `json:"path"` // relative to DataDir
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"modTime"`
	IsActive   bool      `json:"isActive"`   // currently being downloaded/seeded
	IsFavorite bool      `json:"isFavorite"` // protected from eviction
	// InfoHash is the torrent's hex-encoded SHA1 info hash. Populated when the
	// torrent is either active or has a persisted .torrent in metainfoDir.
	// Empty string when we can't resolve the hash — the UI hides Play in that case.
	InfoHash string `json:"infoHash,omitempty"`
}

// CacheStats summarizes disk usage of the streaming cache.
type CacheStats struct {
	DataDir   string       `json:"dataDir"`
	TotalSize int64        `json:"totalSize"`
	MaxSize   int64        `json:"maxSize"`   // 0 = unlimited
	NumActive int          `json:"numActive"` // currently loaded torrents
	Entries   []CacheEntry `json:"entries"`
	// Filesystem footprint of the disk hosting DataDir (0 = statfs unavailable).
	DiskFree  int64 `json:"diskFree"`
	DiskTotal int64 `json:"diskTotal"`
	// Lifetime LRU eviction counters (since process start).
	EvictedCount   int64      `json:"evictedCount"`
	EvictedBytes   int64      `json:"evictedBytes"`
	LastEvictionAt *time.Time `json:"lastEvictionAt,omitempty"`
}

// Stats walks the DataDir and returns disk usage stats.
// "Active" entries are torrents currently loaded in memory (likely being read).
func (s *Streamer) Stats() (*CacheStats, error) {
	st := &CacheStats{
		DataDir: s.cfg.DataDir,
		MaxSize: s.cfg.MaxCacheSize,
	}

	activeNames, nameToHash, numActive := s.buildActiveMaps()
	st.NumActive = numActive

	s.augmentNameToHashFromMetainfo(nameToHash)

	entries, err := os.ReadDir(s.cfg.DataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return nil, err
	}

	for _, ent := range entries {
		full := filepath.Join(s.cfg.DataDir, ent.Name())
		size, mtime, err := dirSizeAndMTime(full)
		if err != nil {
			continue
		}
		st.Entries = append(st.Entries, CacheEntry{
			Path:       ent.Name(),
			Size:       size,
			ModTime:    mtime,
			IsActive:   activeNames[ent.Name()],
			IsFavorite: s.favs != nil && s.favs.IsFavorite(ent.Name()),
			InfoHash:   nameToHash[ent.Name()],
		})
		st.TotalSize += size
	}

	sort.Slice(st.Entries, func(i, j int) bool {
		return st.Entries[i].ModTime.After(st.Entries[j].ModTime)
	})

	st.DiskFree, st.DiskTotal = diskutil.Usage(s.cfg.DataDir)

	s.evictMu.Lock()
	st.EvictedCount = s.evictedCount
	st.EvictedBytes = s.evictedBytes
	if !s.lastEvictionAt.IsZero() {
		last := s.lastEvictionAt
		st.LastEvictionAt = &last
	}
	s.evictMu.Unlock()

	return st, nil
}

func (s *Streamer) buildActiveMaps() (map[string]bool, map[string]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	activeNames := make(map[string]bool, len(s.active))
	nameToHash := make(map[string]string, len(s.active))
	for h, e := range s.active {
		name := e.t.Name()
		activeNames[name] = true
		nameToHash[name] = h.HexString()
	}
	return activeNames, nameToHash, len(s.active)
}

func (s *Streamer) augmentNameToHashFromMetainfo(nameToHash map[string]string) {
	if s.metainfoDir == "" {
		return
	}
	mEnts, err := os.ReadDir(s.metainfoDir)
	if err != nil {
		return
	}
	for _, m := range mEnts {
		if m.IsDir() || !strings.HasSuffix(m.Name(), ".torrent") {
			continue
		}
		mi, err := metainfo.LoadFromFile(filepath.Join(s.metainfoDir, m.Name()))
		if err != nil {
			continue
		}
		info, err := mi.UnmarshalInfo()
		if err != nil || info.Name == "" {
			continue
		}
		if _, ok := nameToHash[info.Name]; !ok {
			nameToHash[info.Name] = mi.HashInfoBytes().HexString()
		}
	}
}

// ClearAll drops every active torrent and wipes the DataDir, *except* favorites.
// Favorites are preserved on disk; their active torrent is dropped but files remain.
func (s *Streamer) ClearAll() error {
	s.mu.Lock()
	for h, e := range s.active {
		e.t.Drop()
		delete(s.active, h)
	}
	s.mu.Unlock()

	entries, err := os.ReadDir(s.cfg.DataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, ent := range entries {
		// Skip favorites + internal bookkeeping files
		name := ent.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if s.favs != nil && s.favs.IsFavorite(name) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(s.cfg.DataDir, name))
	}
	return nil
}

// ClearEntry removes a specific cache entry from disk (by relative path).
// Refuses if the entry is favorited (use Favorites().Remove first).
// If the torrent is currently active, it is dropped first.
func (s *Streamer) ClearEntry(name string) error {
	if s.favs != nil && s.favs.IsFavorite(name) {
		return fmt.Errorf("entry %q é favorito — desfavorite antes de remover", name)
	}
	// Drop matching active torrent if any
	s.mu.Lock()
	for h, e := range s.active {
		if e.t.Name() == name {
			e.t.Drop()
			delete(s.active, h)
			break
		}
	}
	s.mu.Unlock()

	full := filepath.Join(s.cfg.DataDir, filepath.Clean(name))
	// Safety: refuse to delete outside DataDir
	abs, err := filepath.Abs(full)
	if err != nil {
		return err
	}
	dirAbs, err := filepath.Abs(s.cfg.DataDir)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(abs, dirAbs+string(os.PathSeparator)) && abs != dirAbs {
		return fmt.Errorf("invalid path")
	}
	return os.RemoveAll(full)
}

// enforceCacheLimit evicts oldest inactive entries until total size <= maxSize.
// Only inactive entries are touched — active torrents are protected.
func (s *Streamer) enforceCacheLimit() {
	if s.cfg.MaxCacheSize <= 0 {
		return
	}
	stats, err := s.Stats()
	if err != nil {
		return
	}
	if stats.TotalSize <= s.cfg.MaxCacheSize {
		return
	}

	// Sort oldest first (LRU based on mtime). Favorites, active torrents, and
	// in-flight background downloads are protected from eviction.
	inactive := make([]CacheEntry, 0, len(stats.Entries))
	for _, e := range stats.Entries {
		if !e.IsActive && !e.IsFavorite && !s.IsDownloadProtected(e.Path) {
			inactive = append(inactive, e)
		}
	}
	sort.Slice(inactive, func(i, j int) bool {
		return inactive[i].ModTime.Before(inactive[j].ModTime)
	})

	s.evictCandidates(inactive, stats.TotalSize)
}

// evictCandidates deletes entries oldest-first until total size drops to/below
// MaxCacheSize. `candidates` are the entries that looked evictable at snapshot
// time; each is re-checked with evictionBlocked under the lock right before
// removal, so one that became active in the gap is skipped instead of deleted.
func (s *Streamer) evictCandidates(candidates []CacheEntry, total int64) {
	current := total
	for _, e := range candidates {
		if current <= s.cfg.MaxCacheSize {
			break
		}
		// Re-check under the lock: a play may have started between the Stats()
		// snapshot and now, loading this entry into s.active. Deleting it then
		// would kill the file out from under an active HLS transcode.
		if s.evictionBlocked(e.Path) {
			continue
		}
		log.Printf("streamer: cache over %s, evicting %s (%s, mtime=%s)",
			fmtBytes(s.cfg.MaxCacheSize), e.Path, fmtBytes(e.Size), e.ModTime.Format(time.RFC3339))
		if err := os.RemoveAll(filepath.Join(s.cfg.DataDir, e.Path)); err == nil {
			current -= e.Size
			s.recordEviction(e.Size)
		}
	}
}

// recordEviction bumps the lifetime eviction counters surfaced by Stats().
func (s *Streamer) recordEviction(bytes int64) {
	s.evictMu.Lock()
	s.evictedCount++
	s.evictedBytes += bytes
	s.lastEvictionAt = time.Now()
	s.evictMu.Unlock()
}

// dirSizeAndMTime returns the *physical* bytes allocated on disk under a path
// (file or dir), plus the newest mtime.
//
// Why physical (not logical): anacrolix writes sparse files — it opens the
// target file and writes only the bytes for completed pieces. The file's
// logical size (info.Size()) is the **final torrent size**, but the actual
// blocks consumed on disk grow progressively as pieces arrive.
//
// For cache eviction and the UI "X / Y used" indicator, what the user cares
// about is the *real* footprint, not the logical placeholder. Reporting
// logical size makes a 10 GB torrent look "fully cached" the moment metadata
// is received, which is why the cache UI looked pre-allocated.
//
// We use the platform-specific allocated-block count when available (POSIX
// stat.Blocks * 512) and fall back to logical size on platforms where the
// syscall data isn't accessible.
func dirSizeAndMTime(path string) (int64, time.Time, error) {
	var size int64
	var mtime time.Time
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += physicalBytes(info)
		}
		if info.ModTime().After(mtime) {
			mtime = info.ModTime()
		}
		return nil
	})
	return size, mtime, err
}

func fmtBytes(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n) / k
	u := 0
	for v >= k && u < len(units)-1 {
		v /= k
		u++
	}
	return fmt.Sprintf("%.2f %s", v, units[u])
}

// firstChars returns up to n characters from s — for error messages without leaking huge URLs.
func firstChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ─── Active torrent controls (Transmission-style) ───────────────────────────

// Pause soft-pauses a torrent by zeroing its max established connections.
// anacrolix lacks a native Pause; this is the closest equivalent — existing
// peers drop off as TCP keepalives expire, and no new peers are accepted.
// On-disk pieces stay, so Resume picks up where we left off.
func (s *Streamer) Pause(hash metainfo.Hash) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.active[hash]
	if !ok {
		return errors.New(errTorrentNotActive)
	}
	if e.paused {
		return nil // idempotent
	}
	e.t.SetMaxEstablishedConns(0)
	e.paused = true
	return nil
}

// Resume re-enables peer connections previously zeroed by Pause. Idempotent.
func (s *Streamer) Resume(hash metainfo.Hash) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.active[hash]
	if !ok {
		return errors.New(errTorrentNotActive)
	}
	if !e.paused {
		return nil
	}
	e.t.SetMaxEstablishedConns(defaultMaxEstablishedConns)
	e.paused = false
	return nil
}

// SetPriority changes the requested piece priority for every file in the
// torrent. anacrolix uses this to bias the request scheduler — "high" pieces
// will be fetched before "normal", which precede "low". Accepted labels:
// "low" | "normal" | "high".
func (s *Streamer) SetPriority(hash metainfo.Hash, label string) error {
	prio, ok := priorityFromLabel(label)
	if !ok {
		return fmt.Errorf("invalid priority %q (want low|normal|high)", label)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, found := s.active[hash]
	if !found {
		return errors.New(errTorrentNotActive)
	}
	for _, f := range e.t.Files() {
		f.SetPriority(prio)
	}
	e.priority = strings.ToLower(label)
	return nil
}

// ActiveList returns a snapshot of every torrent currently loaded by the
// streamer, formatted for the Transmission-style downloads UI. Each entry has
// rate samples taken under the streamer lock so the numbers are consistent
// across the slice.
func (s *Streamer) ActiveList() []*TorrentInfo {
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.active))
	for _, e := range s.active {
		entries = append(entries, e)
	}
	s.mu.Unlock()

	out := make([]*TorrentInfo, 0, len(entries))
	for _, e := range entries {
		info := s.buildInfo(e)
		s.mu.Lock()
		info.Status = statusForLocked(e)
		info.Priority = e.priority
		s.mu.Unlock()
		out = append(out, info)
	}
	// Deterministic order — by name — avoids the UI rows shuffling on each poll.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// PauseAll soft-pauses every active torrent. Returns the count of newly paused
// torrents (already-paused ones are not double-counted).
func (s *Streamer) PauseAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.active {
		if e.paused {
			continue
		}
		e.t.SetMaxEstablishedConns(0)
		e.paused = true
		n++
	}
	return n
}

// ResumeAll re-enables every soft-paused torrent.
func (s *Streamer) ResumeAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.active {
		if !e.paused {
			continue
		}
		e.t.SetMaxEstablishedConns(defaultMaxEstablishedConns)
		e.paused = false
		n++
	}
	return n
}

// RateLimits exposes the configured global bandwidth caps in bytes/sec.
// A value of 0 means unlimited.
func (s *Streamer) RateLimits() (down, up int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return limiterBytes(s.dlLimiter), limiterBytes(s.upLimiter)
}

func (s *Streamer) ListenPort() int {
	return s.cfg.ListenPort
}

// SetRateLimits updates the global download/upload bandwidth caps in bytes/sec.
// 0 = unlimited. Takes effect immediately — anacrolix re-reads the limiter on
// every chunk transfer.
func (s *Streamer) SetRateLimits(down, up int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	applyLimiter(s.dlLimiter, down)
	applyLimiter(s.upLimiter, up)
	s.cfg.MaxDownloadRate = down
	s.cfg.MaxUploadRate = up
}

// statusForLocked returns the Transmission-style status label. Caller holds s.mu.
func statusForLocked(e *entry) string {
	if e.paused {
		return "paused"
	}
	t := e.t
	if t.Info() == nil {
		return "fetching-metadata"
	}
	if t.BytesCompleted() >= t.Length() && t.Length() > 0 {
		if t.Seeding() {
			return "seeding"
		}
		return "complete"
	}
	return "downloading"
}

// priorityFromLabel parses the user-facing string into an anacrolix priority.
func priorityFromLabel(label string) (types.PiecePriority, bool) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "none":
		return types.PiecePriorityNone, true
	case "low":
		return types.PiecePriorityNormal, true
	case "", "normal":
		return types.PiecePriorityHigh, true
	case "high":
		return types.PiecePriorityNow, true
	}
	return 0, false
}

func labelFromPriority(prio types.PiecePriority) string {
	switch prio {
	case types.PiecePriorityNone:
		return "none"
	case types.PiecePriorityNormal:
		return "low"
	case types.PiecePriorityHigh:
		return "normal"
	case types.PiecePriorityNow:
		return "high"
	default:
		return "normal"
	}
}

// SetFilePriority changes the priority of a single file in the active torrent.
func (s *Streamer) SetFilePriority(hash metainfo.Hash, fileIdx int, label string) error {
	prio, ok := priorityFromLabel(label)
	if !ok {
		return fmt.Errorf("invalid priority %q (want none|low|normal|high)", label)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, found := s.active[hash]
	if !found {
		return errors.New(errTorrentNotActive)
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return fmt.Errorf(errFileIndexOutOfRange, fileIdx)
	}
	files[fileIdx].SetPriority(prio)
	return nil
}

// rateFromBytes converts a bytes/sec setting into a rate.Limit suitable for
// the anacrolix limiter. 0 means unlimited (rate.Inf).
func rateFromBytes(bps int64) rate.Limit {
	if bps <= 0 {
		return rate.Inf
	}
	return rate.Limit(bps)
}

// rateBurst picks a burst (token bucket size) appropriate for the given limit.
// anacrolix's docstring asks for "bigger than the largest Read" — chunks are
// at most 16 KiB plus the internal buffer of ~4 KiB, so a 64 KiB burst is
// safe and lets short spikes through without stalling the scheduler.
func rateBurst(bps int64) int {
	if bps <= 0 {
		return 1 << 16 // any non-zero burst works when limit is Inf
	}
	burst := int(bps / 4) // ~250ms worth of bytes
	const minBurst = 64 * 1024
	if burst < minBurst {
		burst = minBurst
	}
	return burst
}

// applyLimiter updates an existing limiter in place — anacrolix reads it on
// every chunk so the change is visible immediately. Setting bps<=0 means
// unlimited (rate.Inf, large burst).
func applyLimiter(l *rate.Limiter, bps int64) {
	if l == nil {
		return
	}
	if bps <= 0 {
		l.SetLimit(rate.Inf)
		l.SetBurst(1 << 16)
		return
	}
	l.SetLimit(rate.Limit(bps))
	l.SetBurst(rateBurst(bps))
}

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

// limiterBytes converts a limiter's current limit back to bytes/sec. Returns
// 0 when the limit is rate.Inf (unlimited).
func limiterBytes(l *rate.Limiter) int64 {
	if l == nil {
		return 0
	}
	lim := l.Limit()
	if lim == rate.Inf {
		return 0
	}
	return int64(lim)
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
