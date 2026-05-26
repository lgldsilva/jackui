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
	return &MetadataCache{db: db}, nil
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

// DefaultMetadataCachePath returns the standard location inside the stream data dir.
func DefaultMetadataCachePath(dataDir string) string {
	return filepath.Join(dataDir, ".metadata-cache.db")
}
