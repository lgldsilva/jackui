// Package watchlist persists per-user search queries that the server polls in
// the background. New results above the user's seeders threshold trigger a
// push notification to the user's ntfy.sh topic.
package watchlist

import (
	"database/sql"
	"errors"
	"time"

	"github.com/luizg/jackui/internal/dbutil"
	_ "modernc.org/sqlite"
)

// Watchlist is one saved search the worker polls periodically.
type Watchlist struct {
	ID          int       `json:"id"`
	UserID      int       `json:"userId"`
	Query       string    `json:"query"`
	Category    string    `json:"category"` // optional Jackett category filter
	MinSeeders  int       `json:"minSeeders"`
	NtfyTopic   string    `json:"ntfyTopic"` // optional override of the global default
	LastChecked time.Time `json:"lastChecked"`
	CreatedAt   time.Time `json:"createdAt"`
	HitCount    int       `json:"hitCount,omitempty"` // computed from watchlist_seen
}

// Hit is a single new torrent detected by the worker for a given watchlist.
type Hit struct {
	InfoHash string    `json:"infoHash"`
	Title    string    `json:"title"`
	Magnet   string    `json:"magnet"`
	Seeders  int       `json:"seeders"`
	Size     int64     `json:"size"`
	SeenAt   time.Time `json:"seenAt"`
}

type Store struct {
	db *sql.DB
}

func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() { s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS watchlists (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id      INTEGER NOT NULL,
			query        TEXT    NOT NULL,
			category     TEXT    NOT NULL DEFAULT '',
			min_seeders  INTEGER NOT NULL DEFAULT 1,
			ntfy_topic   TEXT    NOT NULL DEFAULT '',
			last_checked DATETIME,
			created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_wl_user ON watchlists(user_id);

		CREATE TABLE IF NOT EXISTS watchlist_seen (
			watchlist_id INTEGER NOT NULL REFERENCES watchlists(id) ON DELETE CASCADE,
			info_hash    TEXT    NOT NULL,
			title        TEXT    NOT NULL DEFAULT '',
			magnet       TEXT    NOT NULL DEFAULT '',
			seeders      INTEGER NOT NULL DEFAULT 0,
			size         INTEGER NOT NULL DEFAULT 0,
			seen_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (watchlist_id, info_hash)
		);
		CREATE INDEX IF NOT EXISTS idx_wls_recent ON watchlist_seen(watchlist_id, seen_at DESC);
	`)
	return err
}

// Create inserts a new watchlist row.
func (s *Store) Create(userID int, query, category string, minSeeders int, ntfyTopic string) (*Watchlist, error) {
	if query == "" {
		return nil, errors.New("query é obrigatória")
	}
	if minSeeders < 0 {
		minSeeders = 0
	}
	res, err := s.db.Exec(`
		INSERT INTO watchlists(user_id, query, category, min_seeders, ntfy_topic)
		VALUES(?, ?, ?, ?, ?)
	`, userID, query, category, minSeeders, ntfyTopic)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.Get(userID, int(id))
}

// Update modifies an existing watchlist owned by userID.
func (s *Store) Update(userID, id int, query, category string, minSeeders int, ntfyTopic string) error {
	if query == "" {
		return errors.New("query é obrigatória")
	}
	res, err := s.db.Exec(`
		UPDATE watchlists SET query=?, category=?, min_seeders=?, ntfy_topic=?
		WHERE id=? AND user_id=?
	`, query, category, minSeeders, ntfyTopic, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("watchlist não encontrada")
	}
	return nil
}

// Delete removes a watchlist (and cascades the seen rows via FK).
func (s *Store) Delete(userID, id int) error {
	res, err := s.db.Exec("DELETE FROM watchlists WHERE id=? AND user_id=?", id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("watchlist não encontrada")
	}
	return nil
}

// Get returns a single watchlist owned by userID.
func (s *Store) Get(userID, id int) (*Watchlist, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, query, category, min_seeders, ntfy_topic,
		       COALESCE(last_checked, ''), created_at
		FROM watchlists WHERE id=? AND user_id=?
	`, id, userID)
	w := &Watchlist{}
	var lastChecked, createdAt string
	if err := row.Scan(&w.ID, &w.UserID, &w.Query, &w.Category, &w.MinSeeders, &w.NtfyTopic, &lastChecked, &createdAt); err != nil {
		return nil, err
	}
	w.LastChecked= dbutil.ParseTime(lastChecked)
	w.CreatedAt= dbutil.ParseTime(createdAt)
	return w, nil
}

// List returns all watchlists for a user, newest first, with hit counts.
func (s *Store) List(userID int) ([]Watchlist, error) {
	rows, err := s.db.Query(`
		SELECT w.id, w.user_id, w.query, w.category, w.min_seeders, w.ntfy_topic,
		       COALESCE(w.last_checked, ''), w.created_at,
		       (SELECT COUNT(*) FROM watchlist_seen WHERE watchlist_id=w.id) AS hits
		FROM watchlists w
		WHERE w.user_id=?
		ORDER BY w.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Watchlist{}
	for rows.Next() {
		w := Watchlist{}
		var lastChecked, createdAt string
		if err := rows.Scan(&w.ID, &w.UserID, &w.Query, &w.Category, &w.MinSeeders, &w.NtfyTopic, &lastChecked, &createdAt, &w.HitCount); err != nil {
			continue
		}
		w.LastChecked= dbutil.ParseTime(lastChecked)
		w.CreatedAt= dbutil.ParseTime(createdAt)
		out = append(out, w)
	}
	return out, rows.Err()
}

// ListAll returns every watchlist across all users — used by the worker.
func (s *Store) ListAll() ([]Watchlist, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, query, category, min_seeders, ntfy_topic,
		       COALESCE(last_checked, ''), created_at
		FROM watchlists
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Watchlist{}
	for rows.Next() {
		w := Watchlist{}
		var lastChecked, createdAt string
		if err := rows.Scan(&w.ID, &w.UserID, &w.Query, &w.Category, &w.MinSeeders, &w.NtfyTopic, &lastChecked, &createdAt); err != nil {
			continue
		}
		w.LastChecked= dbutil.ParseTime(lastChecked)
		w.CreatedAt= dbutil.ParseTime(createdAt)
		out = append(out, w)
	}
	return out, rows.Err()
}

// MarkSeen records an info_hash as already seen. Returns true if this was the
// first time (i.e., a new hit), false if it was already known.
func (s *Store) MarkSeen(watchlistID int, infoHash, title, magnet string, seeders int, size int64) (isNew bool, err error) {
	res, err := s.db.Exec(`
		INSERT OR IGNORE INTO watchlist_seen(watchlist_id, info_hash, title, magnet, seeders, size)
		VALUES(?, ?, ?, ?, ?, ?)
	`, watchlistID, infoHash, title, magnet, seeders, size)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// MarkChecked refreshes last_checked to now — call after a worker pass.
func (s *Store) MarkChecked(watchlistID int) error {
	_, err := s.db.Exec(`UPDATE watchlists SET last_checked=CURRENT_TIMESTAMP WHERE id=?`, watchlistID)
	return err
}

// Hits returns the most recent hits for a watchlist owned by userID.
func (s *Store) Hits(userID, watchlistID, limit int) ([]Hit, error) {
	// Ownership check first
	if _, err := s.Get(userID, watchlistID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT info_hash, title, magnet, seeders, size, seen_at
		FROM watchlist_seen
		WHERE watchlist_id=?
		ORDER BY seen_at DESC LIMIT ?
	`, watchlistID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Hit{}
	for rows.Next() {
		h := Hit{}
		var seenAt string
		if err := rows.Scan(&h.InfoHash, &h.Title, &h.Magnet, &h.Seeders, &h.Size, &seenAt); err != nil {
			continue
		}
		h.SeenAt= dbutil.ParseTime(seenAt)
		out = append(out, h)
	}
	return out, rows.Err()
}
