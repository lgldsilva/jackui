package streamer

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/lgldsilva/jackui/internal/dbutil"
)

// MetadataCache persists TorrentInfo snapshots keyed by info_hash so the UI can
// render the file list + name *instantly* when reopening a torrent the user has
// touched before, even before anacrolix finishes its DHT metadata fetch.
//
// Why this exists: a magnet-only torrent takes 3-10s to resolve to .torrent
// metadata from peers/DHT on each fresh load. The first time we accept the
// delay; subsequent opens of the same hash should be ~instant. Metadata is
// immutable per info_hash so we can keep entries forever — only the on-demand
// torrent client load takes any time at all.
type MetadataCache struct {
	db *dbutil.DB
}

// CachedMeta is the minimal shape we cache (a strict subset of TorrentInfo).
// Rates and per-file Downloaded/Progress are runtime-only and NOT cached —
// they would be stale and misleading.
type CachedMeta struct {
	InfoHash    string       `json:"infoHash"`
	Name        string       `json:"name"`
	TotalSize   int64        `json:"totalSize"`
	Files       []CachedFile `json:"files"`
	PrimaryFile int          `json:"primaryFile"`
	CachedAt    time.Time    `json:"cachedAt"`
}

type CachedFile struct {
	Index   int    `json:"index"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	IsVideo bool   `json:"isVideo"`
}

// CachedArt is the resolved thumbnail for a torrent, persisted per info_hash so
// we never re-run the (expensive) resolution chain — embedded torrent image,
// TMDB lookup, or a captured video frame. Stored alongside the metadata row but
// updated via SetArt() with a disjoint column set, so caching metadata never
// clobbers the art and vice-versa.
type CachedArt struct {
	// Source is "torrent" | "tmdb" | "frame" — the chain step that produced it.
	Source string `json:"source"`
	// Path is the DataDir-relative file for byte-backed sources (torrent/frame).
	// Empty for tmdb (the image lives on the remote CDN at PosterURL).
	Path string `json:"path,omitempty"`
	// PosterURL is the remote image for source=="tmdb"; the handler 302s to it.
	PosterURL string `json:"posterUrl,omitempty"`
	TmdbID    int    `json:"tmdbId,omitempty"`
	ImdbID    string `json:"imdbId,omitempty"`
}

// ArtSourceRank orders art sources by trustworthiness so resolution only ever
// *upgrades* a persisted thumbnail (uploader-curated image > matched poster >
// web search > raw frame). The web search is a fallback for content TMDB can't
// match (adult/obscure) and ranks above a raw frame but below a real poster.
// Exported so the resolver in the handlers layer shares the order.
func ArtSourceRank(source string) int {
	switch source {
	case "torrent":
		return 4
	case "tmdb":
		return 3
	case "web":
		return 2
	case "frame":
		return 1
	default:
		return 0
	}
}

// NewMetadataCache wires the metadata cache onto the shared Postgres pool.
// Schema is applied centrally (internal/db migrations).
func NewMetadataCache(pool *sql.DB) (*MetadataCache, error) {
	return &MetadataCache{db: dbutil.Wrap(pool)}, nil
}

// Close is a no-op: the shared pool's lifecycle is owned by main.
func (m *MetadataCache) Close() error { return nil }

// Get returns a cached snapshot or nil if not present. Never returns error for
// "not found" — the caller treats nil as "no cache" and falls through to the
// live torrent client.
func (m *MetadataCache) Get(infoHash string) *CachedMeta {
	if m == nil {
		return nil
	}
	row := m.db.QueryRow(`SELECT name, total_size, files, primary_file, cached_at FROM metadata WHERE info_hash = ?`, infoHash)
	var cm CachedMeta
	var filesJSON string
	if err := row.Scan(&cm.Name, &cm.TotalSize, &filesJSON, &cm.PrimaryFile, &cm.CachedAt); err != nil {
		return nil
	}
	cm.InfoHash = infoHash
	if err := json.Unmarshal([]byte(filesJSON), &cm.Files); err != nil {
		return nil
	}
	return &cm
}

// Set saves a snapshot. Called by Streamer.Add() once anacrolix delivers
// metadata, so subsequent opens of the same hash hit the cache.
func (m *MetadataCache) Set(info *TorrentInfo) error {
	if m == nil || info == nil {
		return nil
	}
	cached := make([]CachedFile, len(info.Files))
	for i, f := range info.Files {
		cached[i] = CachedFile{Index: f.Index, Path: f.Path, Size: f.Size, IsVideo: f.IsVideo}
	}
	filesJSON, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	_, err = m.db.Exec(`
		INSERT INTO metadata(info_hash, name, total_size, files, primary_file)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(info_hash) DO UPDATE SET
			name = excluded.name,
			total_size = excluded.total_size,
			files = excluded.files,
			primary_file = excluded.primary_file,
			cached_at = CURRENT_TIMESTAMP
	`, info.InfoHash, info.Name, info.TotalSize, string(filesJSON), info.PrimaryFile)
	return err
}

// GetArt returns the persisted thumbnail for an info_hash, or nil when none has
// been resolved yet. Never errors on "not found" — callers treat nil as "no art".
func (m *MetadataCache) GetArt(infoHash string) *CachedArt {
	if m == nil {
		return nil
	}
	row := m.db.QueryRow(`SELECT art_source, art_path, poster_url, tmdb_id, imdb_id FROM metadata WHERE info_hash = ?`, infoHash)
	var ca CachedArt
	if err := row.Scan(&ca.Source, &ca.Path, &ca.PosterURL, &ca.TmdbID, &ca.ImdbID); err != nil {
		return nil
	}
	if ca.Source == "" {
		return nil // row exists (metadata cached) but art never resolved
	}
	return &ca
}

// SetArt persists a resolved thumbnail. Uses a column set disjoint from Set()
// so it neither requires nor clobbers the metadata snapshot — an art-only row
// (name=”) is created if the torrent's metadata hasn't been cached yet.
func (m *MetadataCache) SetArt(infoHash string, art *CachedArt) error {
	if m == nil || art == nil {
		return nil
	}
	_, err := m.db.Exec(`
		INSERT INTO metadata(info_hash, name, art_source, art_path, poster_url, tmdb_id, imdb_id, art_checked_at)
		VALUES(?, '', ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(info_hash) DO UPDATE SET
			art_source     = excluded.art_source,
			art_path       = excluded.art_path,
			poster_url     = excluded.poster_url,
			tmdb_id        = excluded.tmdb_id,
			imdb_id        = excluded.imdb_id,
			art_checked_at = CURRENT_TIMESTAMP
	`, infoHash, art.Source, art.Path, art.PosterURL, art.TmdbID, art.ImdbID)
	return err
}

// ArtSourceNone marks "the resolve chain ran and found nothing". Persisted (with
// a timestamp via SetArt) so ResolveArt doesn't re-run the whole AI+TMDB+web
// chain on every card render for a title that has no art — see ArtNegativeFresh.
const ArtSourceNone = "none"

// ArtNegativeFresh reports whether a recent "no art found" marker exists, so the
// caller can short-circuit instead of re-running the expensive resolve chain.
// A real art source (rank > 0) is never treated as a negative marker.
func (m *MetadataCache) ArtNegativeFresh(infoHash string, ttl time.Duration) bool {
	if m == nil {
		return false
	}
	var source string
	var checkedAt sql.NullTime
	row := m.db.QueryRow(`SELECT art_source, art_checked_at FROM metadata WHERE info_hash = ?`, infoHash)
	if err := row.Scan(&source, &checkedAt); err != nil {
		return false
	}
	if source != ArtSourceNone || !checkedAt.Valid || checkedAt.Time.IsZero() {
		return false
	}
	return time.Since(checkedAt.Time) < ttl
}

// CachedHealth is the last-known swarm health for a torrent, persisted so a card
// can show a prior estimate (with its age) instantly while a fresh probe runs.
type CachedHealth struct {
	Seeders   int       `json:"seeders"`
	Peers     int       `json:"peers"`
	Available bool      `json:"available"` // seeders>0 || peers>0
	CheckedAt time.Time `json:"checkedAt"`
}

// GetHealth returns the persisted swarm health, or nil if never probed.
func (m *MetadataCache) GetHealth(infoHash string) *CachedHealth {
	if m == nil {
		return nil
	}
	row := m.db.QueryRow(`SELECT health_seeders, health_peers, health_checked_at FROM metadata WHERE info_hash = ?`, infoHash)
	var seeders, peers int
	var checkedAt sql.NullTime
	if err := row.Scan(&seeders, &peers, &checkedAt); err != nil {
		return nil
	}
	if !checkedAt.Valid {
		return nil // row exists but health never probed
	}
	return &CachedHealth{
		Seeders:   seeders,
		Peers:     peers,
		Available: seeders > 0 || peers > 0,
		CheckedAt: checkedAt.Time,
	}
}

// SetHealth persists a swarm-health probe result (disjoint columns — creates an
// health-only row if the torrent's metadata isn't cached yet).
func (m *MetadataCache) SetHealth(infoHash string, seeders, peers int) error {
	if m == nil {
		return nil
	}
	_, err := m.db.Exec(`
		INSERT INTO metadata(info_hash, name, health_seeders, health_peers, health_checked_at)
		VALUES(?, '', ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(info_hash) DO UPDATE SET
			health_seeders    = excluded.health_seeders,
			health_peers      = excluded.health_peers,
			health_checked_at = CURRENT_TIMESTAMP
	`, infoHash, seeders, peers)
	return err
}

// SortMeta is the size+seeders pair used to sort the favorites list.
// Seeders is -1 when the swarm was never probed (so it sorts last).
type SortMeta struct {
	TotalSize int64
	Seeders   int
}

// GetSortMeta returns size+seeders for the given hashes in a single query, keyed
// by info_hash. Hashes with no cached row are simply absent from the map. Used to
// enrich the favorites list for sorting without a cross-DB JOIN.
func (m *MetadataCache) GetSortMeta(hashes []string) map[string]SortMeta {
	out := map[string]SortMeta{}
	if m == nil || len(hashes) == 0 {
		return out
	}
	placeholders := make([]string, len(hashes))
	args := make([]any, len(hashes))
	for i, h := range hashes {
		placeholders[i] = "?"
		args[i] = h
	}
	rows, err := m.db.Query(
		`SELECT info_hash, total_size, health_seeders FROM metadata WHERE info_hash IN (`+strings.Join(placeholders, ",")+`)`,
		args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		var sm SortMeta
		if err := rows.Scan(&hash, &sm.TotalSize, &sm.Seeders); err != nil {
			continue
		}
		out[hash] = sm
	}
	return out
}

// DefaultMetadataCachePath returns the standard location inside the stream data dir.
func DefaultMetadataCachePath(dataDir string) string {
	return filepath.Join(dataDir, ".metadata-cache.db")
}
