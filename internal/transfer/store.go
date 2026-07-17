package transfer

import (
	"database/sql"

	"github.com/lgldsilva/jackui/internal/dbutil"
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

// Store persists pending transfers in PostgreSQL (shared pool).
// nil-safe so callers can stay agnostic to whether persistence is wired.
type Store struct {
	db *dbutil.DB
}

// OpenStore wires the pending-transfers store onto the shared Postgres pool.
// Schema is applied centrally (internal/db migrations).
func OpenStore(pool *sql.DB) (*Store, error) {
	return &Store{db: dbutil.Wrap(pool)}, nil
}

// Close is a no-op: the shared pool's lifecycle is owned by main.

// Add persists a pending transfer and returns its id (0 when the store is nil).
func (s *Store) Add(p Pending) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO pending_transfers (kind, src, dst, payload) VALUES (?, ?, ?, ?) RETURNING id`,
		p.Kind, p.Src, p.Dst, p.Payload,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
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
