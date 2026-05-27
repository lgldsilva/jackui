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
	"net/http"
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
	"github.com/anacrolix/torrent/types"
	"golang.org/x/time/rate"
)

// defaultMaxEstablishedConns mirrors the anacrolix client default. We re-apply
// this on Resume() after a Pause() temporarily zeroed it.
const defaultMaxEstablishedConns = 80

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
}

type Streamer struct {
	cfg    Config
	client *torrent.Client
	mu     sync.Mutex
	active map[metainfo.Hash]*entry
	favs   *FavoritesStore // optional — nil disables favorites protection
	cache  *MetadataCache  // optional — nil disables instant-open snapshots
	stop   chan struct{}
	// downloads holds torrent names that are part of a background-download
	// queue. They must NOT be evicted by enforceCacheLimit even when idle —
	// the user is waiting for the file to finish. The downloads worker
	// (internal/downloads) maintains this set via RegisterDownload /
	// UnregisterDownload.
	downloads map[string]struct{}
	// metainfoDir holds serialized .torrent files keyed by info_hash so that
	// re-opening a previously-seen magnet skips the DHT metadata round-trip.
	metainfoDir string
	// Global bandwidth limiters wired into the anacrolix client config. Mutated
	// in place via SetLimit/SetBurst — anacrolix re-reads the limit on every
	// chunk read/write.
	dlLimiter *rate.Limiter
	upLimiter *rate.Limiter
}

// SetFavorites attaches the favorites store. Must be called before any GC tick.
func (s *Streamer) SetFavorites(f *FavoritesStore) { s.favs = f }

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
func (s *Streamer) IsDownloadProtected(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.downloads[name]
	return ok
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

// Favorites returns the attached store (may be nil).
func (s *Streamer) Favorites() *FavoritesStore { return s.favs }

// SetMetadataCache attaches the metadata snapshot cache. Optional — when set,
// every successful Add() persists the file list so the UI can render it
// instantly next time the same info_hash is opened.
func (s *Streamer) SetMetadataCache(c *MetadataCache) { s.cache = c }

// MetadataCache returns the attached cache (may be nil).
func (s *Streamer) MetadataCache() *MetadataCache { return s.cache }

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
}

// FileInfo is the JSON-friendly view of a file inside a torrent.
type FileInfo struct {
	Index       int     `json:"index"`
	Path        string  `json:"path"`
	Size        int64   `json:"size"`
	IsVideo     bool    `json:"isVideo"`
	Downloaded  int64   `json:"downloaded"`
	Progress    float64 `json:"progress"` // 0..1
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
	Priority string `json:"priority,omitempty"`
}

// GlobalRate aggregates download/upload rates across all active torrents.
type GlobalRate struct {
	DownRate       int64 `json:"downRate"`
	UpRate         int64 `json:"upRate"`
	ActiveTorrents int   `json:"activeTorrents"`
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

	tcfg := torrent.NewDefaultClientConfig()
	tcfg.DataDir = cfg.DataDir
	tcfg.Seed = false
	tcfg.NoUpload = true
	// Reduce log noise
	tcfg.Logger = tcfg.Logger.WithFilterLevel(alog.Critical)

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
		cfg:         cfg,
		client:      client,
		active:      make(map[metainfo.Hash]*entry),
		stop:        make(chan struct{}),
		downloads:   make(map[string]struct{}),
		metainfoDir: metainfoDir,
		dlLimiter:   dlLimiter,
		upLimiter:   upLimiter,
	}

	go s.gcLoop()
	return s, nil
}

// metainfoPath returns the on-disk location for a torrent's serialized
// .torrent (its `metainfo.MetaInfo`). One file per info hash.
func (s *Streamer) metainfoPath(h metainfo.Hash) string {
	return filepath.Join(s.metainfoDir, h.HexString()+".torrent")
}

// ParseMagnet validates a magnet URI and extracts its info hash + display
// name without touching the network. Used by the import flow to preview what
// a pasted magnet resolves to before committing it to favorites.
func (s *Streamer) ParseMagnet(magnet string) (hash, name string, err error) {
	if i := strings.Index(magnet, "magnet:"); i >= 0 {
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
	// file that loadCachedMetainfo would treat as garbage).
	f, err := os.CreateTemp(s.metainfoDir, ".tmp-*.torrent")
	if err != nil {
		return
	}
	if err := mi.Write(f); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return
	}
	_ = os.Rename(f.Name(), path)
}

func (s *Streamer) Close() {
	close(s.stop)
	s.client.Close()
}

// Add loads a magnet OR an HTTP(S) URL to a .torrent file and waits for metadata.
// Returns the torrent info once available.
//
// For .torrent URLs (common in private trackers and some Jackett providers that
// don't return a magnet), we fetch the file, parse the metainfo, and add via
// AddTorrentSpec — same downstream behavior as magnet.
func (s *Streamer) Add(ctx context.Context, magnetOrURL string) (*TorrentInfo, error) {
	// Defensive cleanup: strip whitespace and BOM (U+FEFF, encoded as \xef\xbb\xbf in UTF-8)
	src := strings.TrimSpace(magnetOrURL)
	src = strings.TrimPrefix(src, "\xef\xbb\xbf")

	var t *torrent.Torrent
	var err error

	// Robust detection: check anywhere in the first 16 chars in case of stray bytes
	lower := strings.ToLower(src[:min(16, len(src))])
	switch {
	case strings.HasPrefix(lower, "magnet:") || strings.Contains(lower, "magnet:"):
		// Find the actual start of "magnet:" in case there's a leading garbage char we missed
		if i := strings.Index(src, "magnet:"); i >= 0 {
			src = src[i:]
		}
		// Fast path: if we've persisted this hash's metainfo before, skip the
		// DHT round-trip and hand the .torrent directly to anacrolix. Cuts
		// "first byte" latency from 3-10s down to ~50ms on cold cache hits.
		if mi, err := metainfo.ParseMagnetUri(src); err == nil {
			if cached := s.loadCachedMetainfo(mi.InfoHash); cached != nil {
				t, err = s.client.AddTorrent(cached)
				if err != nil {
					return nil, fmt.Errorf("add cached metainfo: %w", err)
				}
				break
			}
		}
		t, err = s.client.AddMagnet(src)
		if err != nil {
			return nil, fmt.Errorf("add magnet: %w", err)
		}
	case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://"):
		t, err = s.addFromTorrentURL(ctx, src)
		if err != nil {
			return nil, fmt.Errorf("add torrent URL: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported source — provide a magnet: or http(s):// URL (got %q)", firstChars(src, 30))
	}

	// Wait for metadata with timeout
	waitCtx, cancel := context.WithTimeout(ctx, s.cfg.MetadataWait)
	defer cancel()
	select {
	case <-t.GotInfo():
	case <-waitCtx.Done():
		return nil, fmt.Errorf("timeout aguardando metadados do torrent (%s)", s.cfg.MetadataWait)
	}

	now := time.Now()
	s.mu.Lock()
	e := &entry{t: t, lastAccess: now, lastSampleAt: now}
	s.active[t.InfoHash()] = e
	s.mu.Unlock()

	// Serialize the metainfo to disk so the next Add() for this hash skips
	// the DHT round-trip. Safe to call after GotInfo — t.Metainfo() is now
	// populated with the full piece-hash table.
	s.persistMetainfo(t)

	info := s.buildInfo(e)
	// Persist the snapshot so subsequent opens of this info_hash are instant —
	// the cache survives container restarts, unlike s.active which is in-memory.
	if s.cache != nil {
		_ = s.cache.Set(info)
	}
	return info, nil
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
func (s *Streamer) addFromTorrentURL(ctx context.Context, torrentURL string) (*torrent.Torrent, error) {
	var capturedMagnet string

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if strings.HasPrefix(strings.ToLower(req.URL.String()), "magnet:") {
				capturedMagnet = req.URL.String()
				return http.ErrUseLastResponse // stop following, return the previous response
			}
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", torrentURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch torrent URL: %w", err)
	}
	defer resp.Body.Close()

	// Case 1: server redirected to a magnet URI → add as magnet
	if capturedMagnet != "" {
		t, merr := s.client.AddMagnet(capturedMagnet)
		if merr != nil {
			return nil, fmt.Errorf("add magnet from redirect: %w", merr)
		}
		return t, nil
	}

	// Case 2: direct .torrent file response
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
		return nil, nil, errors.New("torrent não está ativo")
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
	// of 4K lookahead so the encoder never starves on a healthy swarm.
	r.SetReadahead(32 << 20) // 32 MiB
	r.SetResponsive()        // prioritize pieces around current read position

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
func (s *Streamer) Drop(hash metainfo.Hash) {
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		delete(s.active, hash)
	}
	s.mu.Unlock()
	if ok {
		e.t.Drop()
	}
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
	type epHit struct{ idx, season, episode int }
	var episodes []epHit
	for _, f := range files {
		if !f.IsVideo {
			continue
		}
		if extraTagsRe.MatchString(f.Path) {
			continue
		}
		m := seriesEpisodeRe.FindStringSubmatch(f.Path)
		if m == nil {
			continue
		}
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		episodes = append(episodes, epHit{idx: f.Index, season: s, episode: e})
	}
	if len(episodes) >= 3 {
		// Pick the lowest (S, E) — that's the natural "play from the start" point.
		best := episodes[0]
		for _, ep := range episodes[1:] {
			if ep.season < best.season || (ep.season == best.season && ep.episode < best.episode) {
				best = ep
			}
		}
		return best.idx
	}
	// Movie-shaped torrent: pick the largest non-extra video.
	largestIdx, largestSize := -1, int64(0)
	for _, f := range files {
		if !f.IsVideo {
			continue
		}
		if extraTagsRe.MatchString(f.Path) {
			continue
		}
		if f.Size > largestSize {
			largestIdx, largestSize = f.Index, f.Size
		}
	}
	if largestIdx >= 0 {
		return largestIdx
	}
	// Last-resort: any video at all (extras included).
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
			s.mu.Lock()
			for h, e := range s.active {
				if now.Sub(e.lastAccess) > s.cfg.IdleTimeout {
					// Active downloads stay alive even when idle — the user
					// is waiting for the file to finish in background.
					if _, protected := s.downloads[e.t.Name()]; protected {
						continue
					}
					log.Printf("streamer: dropping idle torrent %s (%s)", e.t.Name(), h.HexString()[:8])
					delete(s.active, h)
					e.t.Drop()
				}
			}
			s.mu.Unlock()
			// Then enforce cache size cap (LRU over inactive entries)
			s.enforceCacheLimit()
		}
	}
}

// ─── Cache management ───────────────────────────────────────────────────────

// CacheEntry describes one item on disk in the cache directory.
type CacheEntry struct {
	Path       string    `json:"path"`               // relative to DataDir
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"modTime"`
	IsActive   bool      `json:"isActive"`           // currently being downloaded/seeded
	IsFavorite bool      `json:"isFavorite"`         // protected from eviction
	// InfoHash is the torrent's hex-encoded SHA1 info hash. Populated when the
	// torrent is either active or has a persisted .torrent in metainfoDir.
	// Empty string when we can't resolve the hash — the UI hides Play in that case.
	InfoHash   string    `json:"infoHash,omitempty"`
}

// CacheStats summarizes disk usage of the streaming cache.
type CacheStats struct {
	DataDir    string       `json:"dataDir"`
	TotalSize  int64        `json:"totalSize"`
	MaxSize    int64        `json:"maxSize"`    // 0 = unlimited
	NumActive  int          `json:"numActive"`  // currently loaded torrents
	Entries    []CacheEntry `json:"entries"`
}

// Stats walks the DataDir and returns disk usage stats.
// "Active" entries are torrents currently loaded in memory (likely being read).
func (s *Streamer) Stats() (*CacheStats, error) {
	st := &CacheStats{
		DataDir: s.cfg.DataDir,
		MaxSize: s.cfg.MaxCacheSize,
	}

	// Build (a) a set of active torrent names so we can flag entries and
	// (b) a name → hex-hash map so callers can re-activate Drop'd torrents
	// (the Play button on the cache list uses this to feed a bare magnet
	// back through /api/stream/add when the torrent is no longer loaded).
	s.mu.Lock()
	activeNames := make(map[string]bool, len(s.active))
	nameToHash := make(map[string]string, len(s.active))
	for h, e := range s.active {
		name := e.t.Name()
		activeNames[name] = true
		nameToHash[name] = h.HexString()
	}
	st.NumActive = len(s.active)
	s.mu.Unlock()

	// Augment the name → hash map with persisted .torrent files. This covers
	// the common case: torrent was Drop'd by GC, files remain on disk, and the
	// UI still wants to offer Play. Best-effort — parse failures are skipped.
	if s.metainfoDir != "" {
		if mEnts, err := os.ReadDir(s.metainfoDir); err == nil {
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
				// Active torrents already filled this slot — don't overwrite.
				if _, ok := nameToHash[info.Name]; !ok {
					nameToHash[info.Name] = mi.HashInfoBytes().HexString()
				}
			}
		}
	}

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

	// Sort newest first
	sort.Slice(st.Entries, func(i, j int) bool {
		return st.Entries[i].ModTime.After(st.Entries[j].ModTime)
	})

	return st, nil
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

	current := stats.TotalSize
	for _, e := range inactive {
		if current <= s.cfg.MaxCacheSize {
			break
		}
		log.Printf("streamer: cache over %s, evicting %s (%s, mtime=%s)",
			fmtBytes(s.cfg.MaxCacheSize), e.Path, fmtBytes(e.Size), e.ModTime.Format(time.RFC3339))
		if err := os.RemoveAll(filepath.Join(s.cfg.DataDir, e.Path)); err == nil {
			current -= e.Size
		}
	}
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
		return errors.New("torrent não está ativo")
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
		return errors.New("torrent não está ativo")
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
		return errors.New("torrent não está ativo")
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
// We deliberately map "low" to Normal (still wanted) instead of None — None
// means "do not download" which is not what a Transmission user expects from
// "low priority". The mapping below biases the scheduler within the wanted band:
//
//   low    -> Normal    (default "wanted")
//   normal -> High      (elevated above other torrents at Normal)
//   high   -> Now       (reader-level urgency)
func priorityFromLabel(label string) (types.PiecePriority, bool) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "low":
		return types.PiecePriorityNormal, true
	case "", "normal":
		return types.PiecePriorityHigh, true
	case "high":
		return types.PiecePriorityNow, true
	}
	return 0, false
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
		active:    make(map[metainfo.Hash]*entry),
		downloads: make(map[string]struct{}),
		dlLimiter: rate.NewLimiter(rate.Inf, 1<<16),
		upLimiter: rate.NewLimiter(rate.Inf, 1<<16),
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
