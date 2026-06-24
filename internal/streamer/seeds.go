package streamer

import (
	"database/sql"
	"path/filepath"
	"time"

	"github.com/lgldsilva/jackui/internal/dbutil"
	_ "modernc.org/sqlite"
)

// SeedsStore persists the torrents that must keep seeding (because they belong
// to a configured seed-tracker, e.g. jackui). On boot the streamer
// re-adds every entry so seeding resumes without the user re-opening anything.
//
// This is separate from FavoritesStore: favorites are a user-facing list that
// only protects pieces from LRU eviction, whereas a seed entry is an automatic,
// tracker-driven marker whose job is to bring the torrent back into the swarm.
type SeedsStore struct {
	db *sql.DB
}

// SeedEntry is one persisted seed row.
type SeedEntry struct {
	InfoHash string    `json:"infoHash"`
	Magnet   string    `json:"magnet"`
	Name     string    `json:"name"`
	AddedAt  time.Time `json:"addedAt"`
}

// NewSeeds opens (or creates) the seeds SQLite DB. Typically
// `<state_dir>/.seeds.db`.
func NewSeeds(path string) (*SeedsStore, error) {
	db, err := sql.Open(dbutil.DriverName, path+dbutil.PragmaWAL+dbutil.PragmaBusy5s)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS seeds (
			info_hash TEXT PRIMARY KEY,
			magnet    TEXT NOT NULL DEFAULT '',
			name      TEXT NOT NULL DEFAULT '',
			added_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		db.Close()
		return nil, err
	}
	return &SeedsStore{db: db}, nil
}

// Close releases the DB handle.
func (s *SeedsStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Add upserts a seed entry. Idempotent — re-adding the same hash refreshes the
// magnet/name without disturbing added_at. Nil-safe receiver.
func (s *SeedsStore) Add(infoHash, magnet, name string) error {
	if s == nil || s.db == nil || infoHash == "" {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO seeds (info_hash, magnet, name) VALUES (?, ?, ?)
		ON CONFLICT(info_hash) DO UPDATE SET magnet=excluded.magnet, name=excluded.name
	`, infoHash, magnet, name)
	return err
}

// Remove deletes a seed entry. Nil-safe receiver.
func (s *SeedsStore) Remove(infoHash string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM seeds WHERE info_hash = ?`, infoHash)
	return err
}

// List returns all persisted seed entries, newest first. Nil-safe receiver.
func (s *SeedsStore) List() ([]SeedEntry, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT info_hash, magnet, name, added_at FROM seeds ORDER BY added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeedEntry
	for rows.Next() {
		var e SeedEntry
		var added string
		if err := rows.Scan(&e.InfoHash, &e.Magnet, &e.Name, &added); err != nil {
			return nil, err
		}
		e.AddedAt = dbutil.ParseTime(added)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Has reports whether a hash is already persisted. Nil-safe receiver.
func (s *SeedsStore) Has(infoHash string) bool {
	if s == nil || s.db == nil {
		return false
	}
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM seeds WHERE info_hash = ?`, infoHash).Scan(&one)
	return err == nil
}

// DefaultSeedsPath returns the standard location inside the state dir.
func DefaultSeedsPath(dataDir string) string {
	return filepath.Join(dataDir, ".seeds.db")
}
