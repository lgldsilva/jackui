package transfer

import (
	"database/sql"

	"github.com/lgldsilva/jackui/internal/dbutil"
	_ "modernc.org/sqlite"
)

// Pending is a transfer (move/promote) whose copy must survive a restart. The
// intent is persisted BEFORE the copy starts and removed when it completes, so a
// boot reconciler can re-submit whatever was interrupted by a deploy/crash. The
// copy itself is resume-aware (copyFileAndRemove skips files already at the
// destination), so a re-submit only finishes what's left — it never re-copies
// what a previous run already moved.
type Pending struct {
	ID      int64
	Kind    string // "promote" | "local-move" | "local-promote"
	Src     string
	Dst     string
	Payload string // kind-specific JSON (promote: downloadID/userID/keepSeeding)
}

// Store persists pending transfers in a dedicated SQLite file. All methods are
// nil-safe so callers can stay agnostic to whether persistence is wired.
type Store struct {
	db *sql.DB
}

// OpenStore opens (creating if needed) the pending-transfers DB at path.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open(dbutil.DriverName, path+dbutil.PragmaWAL+dbutil.PragmaFK+dbutil.PragmaBusy5s)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() {
	if s != nil && s.db != nil {
		_ = s.db.Close()
	}
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS pending_transfers (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			kind       TEXT     NOT NULL,
			src        TEXT     NOT NULL,
			dst        TEXT     NOT NULL,
			payload    TEXT     NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`)
	return err
}

// Add persists a pending transfer and returns its id (0 when the store is nil).
func (s *Store) Add(p Pending) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	res, err := s.db.Exec(
		`INSERT INTO pending_transfers (kind, src, dst, payload) VALUES (?, ?, ?, ?)`,
		p.Kind, p.Src, p.Dst, p.Payload,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Remove deletes a pending transfer once its copy completed (no-op on id 0).
func (s *Store) Remove(id int64) error {
	if s == nil || s.db == nil || id == 0 {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM pending_transfers WHERE id = ?`, id)
	return err
}

// List returns every pending transfer in insertion order (oldest first).
func (s *Store) List() ([]Pending, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT id, kind, src, dst, payload FROM pending_transfers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Pending
	for rows.Next() {
		var p Pending
		if err := rows.Scan(&p.ID, &p.Kind, &p.Src, &p.Dst, &p.Payload); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
