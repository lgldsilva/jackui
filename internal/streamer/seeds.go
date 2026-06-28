package streamer

import (
	"database/sql"
	"path/filepath"
	"time"

	"github.com/lgldsilva/jackui/internal/dbutil"
)

// SeedsStore persists the torrents that must keep seeding (because they belong
// to a configured seed-tracker, e.g. jackui). On boot the streamer
// re-adds every entry so seeding resumes without the user re-opening anything.
//
// This is separate from FavoritesStore: favorites are a user-facing list that
// only protects pieces from LRU eviction, whereas a seed entry is an automatic,
// tracker-driven marker whose job is to bring the torrent back into the swarm.
type SeedsStore struct {
	db *dbutil.DB
}

// SeedEntry is one persisted seed row.
type SeedEntry struct {
	InfoHash string    `json:"infoHash"`
	Magnet   string    `json:"magnet"`
	Name     string    `json:"name"`
	AddedAt  time.Time `json:"addedAt"`
}

// NewSeeds wires the seeds store onto the shared Postgres pool. Schema is
// applied centrally (internal/db migrations).
func NewSeeds(pool *sql.DB) (*SeedsStore, error) {
	return &SeedsStore{db: dbutil.Wrap(pool)}, nil
}

// Close is a no-op: the shared pool's lifecycle is owned by main.
func (s *SeedsStore) Close() error { return nil }

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
		if err := rows.Scan(&e.InfoHash, &e.Magnet, &e.Name, &e.AddedAt); err != nil {
			return nil, err
		}
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
