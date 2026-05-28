package streamer

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/luizg/jackui/internal/dbutil"
	_ "modernc.org/sqlite"
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
	db *sql.DB
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

func NewMetadataCache(path string) (*MetadataCache, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS metadata (
			info_hash  TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			total_size INTEGER NOT NULL DEFAULT 0,
			files      TEXT NOT NULL DEFAULT '[]',
			primary_file INTEGER NOT NULL DEFAULT -1,
			cached_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		db.Close()
		return nil, err
	}
	// Art columns added in a later version — migrate idempotently so existing
	// caches keep working. Each is a no-op once the column exists.
	for col, ddl := range map[string]string{
		"art_source": `ALTER TABLE metadata ADD COLUMN art_source TEXT NOT NULL DEFAULT ''`,
		"art_path":   `ALTER TABLE metadata ADD COLUMN art_path TEXT NOT NULL DEFAULT ''`,
		"poster_url": `ALTER TABLE metadata ADD COLUMN poster_url TEXT NOT NULL DEFAULT ''`,
		"tmdb_id":    `ALTER TABLE metadata ADD COLUMN tmdb_id INTEGER NOT NULL DEFAULT 0`,
		"imdb_id":    `ALTER TABLE metadata ADD COLUMN imdb_id TEXT NOT NULL DEFAULT ''`,
	} {
		if !columnExists(db, "metadata", col) {
			if _, err := db.Exec(ddl); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	return &MetadataCache{db: db}, nil
}

// columnExists reports whether a table already has a column, so ADD COLUMN
// migrations stay idempotent (SQLite has no "ADD COLUMN IF NOT EXISTS").
func columnExists(db *sql.DB, table, column string) bool {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil && name == column {
			return true
		}
	}
	return false
}

func (m *MetadataCache) Close() error {
	if m == nil {
		return nil
	}
	return m.db.Close()
}

// Get returns a cached snapshot or nil if not present. Never returns error for
// "not found" — the caller treats nil as "no cache" and falls through to the
// live torrent client.
func (m *MetadataCache) Get(infoHash string) *CachedMeta {
	if m == nil {
		return nil
	}
	row := m.db.QueryRow(`SELECT name, total_size, files, primary_file, cached_at FROM metadata WHERE info_hash = ?`, infoHash)
	var cm CachedMeta
	var filesJSON, cachedAt string
	if err := row.Scan(&cm.Name, &cm.TotalSize, &filesJSON, &cm.PrimaryFile, &cachedAt); err != nil {
		return nil
	}
	cm.InfoHash = infoHash
	cm.CachedAt = dbutil.ParseTime(cachedAt)
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
// (name='') is created if the torrent's metadata hasn't been cached yet.
func (m *MetadataCache) SetArt(infoHash string, art *CachedArt) error {
	if m == nil || art == nil {
		return nil
	}
	_, err := m.db.Exec(`
		INSERT INTO metadata(info_hash, name, art_source, art_path, poster_url, tmdb_id, imdb_id)
		VALUES(?, '', ?, ?, ?, ?, ?)
		ON CONFLICT(info_hash) DO UPDATE SET
			art_source = excluded.art_source,
			art_path   = excluded.art_path,
			poster_url = excluded.poster_url,
			tmdb_id    = excluded.tmdb_id,
			imdb_id    = excluded.imdb_id
	`, infoHash, art.Source, art.Path, art.PosterURL, art.TmdbID, art.ImdbID)
	return err
}

// DefaultMetadataCachePath returns the standard location inside the stream data dir.
func DefaultMetadataCachePath(dataDir string) string {
	return filepath.Join(dataDir, ".metadata-cache.db")
}
