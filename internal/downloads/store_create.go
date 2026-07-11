package downloads

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// execer is the subset of *sql.DB / *sql.Tx that createOne needs. Sharing it lets
// Create (autocommit) and BatchCreate (single transaction) run the SAME idempotent
// insert logic — no duplicated SQL, and a partial failure inside a batch can roll
// the whole thing back (all-or-nothing).
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// createOne runs the idempotent enqueue for a single row against `x` (a DB or a
// Tx): validate → re-queue an existing paused/failed row (GetByKey) → otherwise
// INSERT. It returns the resulting row plus `inserted` (true only for a fresh
// INSERT; false when an existing row was returned/re-queued). BOTH reads and
// writes go through `x`: in a batch (x is an open Tx) the idempotency read must
// SEE the rows inserted earlier in the SAME transaction, so it can't read off
// s.db (a different connection that wouldn't see the uncommitted writes).
func (s *Store) createOne(x execer, d Download) (row *Download, inserted bool, err error) {
	if d.InfoHash == "" || d.Magnet == "" {
		return nil, false, errors.New("infoHash e magnet são obrigatórios")
	}
	if d.FileIndex < FileIndexWholeTorrent {
		return nil, false, fmt.Errorf("invalid fileIndex %d (min %d)", d.FileIndex, FileIndexWholeTorrent)
	}
	priority := d.Priority
	if !validPriority(priority) {
		priority = PriorityNormal
	}
	// Try to fetch existing first — idempotent enqueue.
	existing, err := s.getByKeyWith(x, d.UserID, d.InfoHash, d.FileIndex)
	if errors.Is(err, sql.ErrNoRows) {
		existing, err = nil, nil
	}
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		if err := requeueExisting(x, existing, d); err != nil {
			return nil, false, err
		}
		return existing, false, nil
	}
	var id int64
	err = x.QueryRow(`
		INSERT INTO downloads(user_id, info_hash, file_index, file_path, file_size, name, magnet, tracker, category, status, priority, source, dest_base, dest_subdir, queued_since)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP) RETURNING id
	`, d.UserID, d.InfoHash, d.FileIndex, d.FilePath, d.FileSize, d.Name, d.Magnet, d.Tracker, d.Category, StatusQueued, priority, d.Source, d.DestBase, d.DestSubdir).Scan(&id)
	if err != nil {
		return nil, false, err
	}
	got, err := s.getWith(x, d.UserID, int(id))
	return got, true, err
}

// requeueExisting applies the idempotent re-enqueue to an already-present row
// (mutating `existing` in place to match): a paused/failed row goes back to the
// queue, and the tracker/category from the new request override. The scheduler
// honors the active limit, so a re-queue never jumps straight to downloading.
func requeueExisting(x execer, existing *Download, d Download) error {
	if existing.Status == StatusPaused || existing.Status == StatusFailed {
		if _, err := x.Exec(`UPDATE downloads SET status=?, error='', queued_since=CURRENT_TIMESTAMP WHERE id=?`, StatusQueued, existing.ID); err != nil {
			return err
		}
		existing.Status = StatusQueued
		existing.Error = ""
	}
	if d.Tracker != "" || d.Category != "" {
		if _, err := x.Exec(`UPDATE downloads SET tracker=?, category=? WHERE id=?`, d.Tracker, d.Category, existing.ID); err != nil {
			return err
		}
		existing.Tracker = d.Tracker
		existing.Category = d.Category
	}
	return nil
}

// Create inserts a new download row in `queued` state. The scheduler promotes it
// to `downloading` once a slot is free (active limit). If a row already exists
// for the (user, info_hash, file_index) tuple, returns it unchanged — re-queueing
// an existing download is idempotent.
func (s *Store) Create(d Download) (*Download, error) {
	row, _, err := s.createOne(s.db, d)
	return row, err
}

// BatchResult reports the outcome of a BatchCreate: every resulting row (in input
// order) plus how many were pre-existing (re-queued / unchanged) vs freshly created.
type BatchResult struct {
	Rows     []*Download
	Created  int // freshly inserted rows
	Requeued int // rows that already existed (idempotent hit)
}

// BatchCreate enqueues N rows (typically every selected file of ONE torrent) in
// a SINGLE transaction: either all rows land or none do. It reuses Create's
// idempotent logic (re-queue paused/failed, dedupe on the UNIQUE key) via
// createOne against the Tx. A failure on any row rolls the whole batch back.
func (s *Store) BatchCreate(rows []Download) (*BatchResult, error) {
	if len(rows) == 0 {
		return nil, errors.New("no rows to create")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	res := &BatchResult{Rows: make([]*Download, 0, len(rows))}
	for _, d := range rows {
		created, inserted, err := s.createOne(tx, d)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		res.Rows = append(res.Rows, created)
		if inserted {
			res.Created++
		} else {
			res.Requeued++
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// EnqueueMagnet creates a queued download from a bare magnet, before any
// torrent metadata is known. FileIndex -1 means "pick the best file" — the
// worker resolves it after GotInfo (same contract as the Transmission RPC
// shim). Used by automation (watchlist auto-download); idempotent via Create.
func (s *Store) EnqueueMagnet(userID int, infoHash, name, magnet, tracker string) error {
	_, err := s.Create(Download{
		UserID:   userID,
		InfoHash: strings.ToLower(infoHash),
		// FileIndex -1: best-file sentinel (see resolveFileIndex in worker.go)
		FileIndex: -1,
		Name:      name,
		Magnet:    magnet,
		Tracker:   tracker,
	})
	return err
}
