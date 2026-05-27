// Package downloads persists per-user background downloads — full-file
// torrent transfers that run to completion regardless of player activity.
//
// Unlike streaming (where pieces are evicted under LRU pressure once the user
// stops watching), a queued download keeps its torrent alive until every byte
// is on disk. Status transitions are durable so the worker can resume across
// process restarts.
package downloads

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/luizg/jackui/internal/dbutil"
	_ "modernc.org/sqlite"
)

// Status is the lifecycle of a Download row. The worker only acts on rows
// in `downloading`; the rest are terminal or user-paused.
const (
	StatusQueued      = "queued"
	StatusDownloading = "downloading"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"
	StatusPaused      = "paused"
)

// Download is one tracked background transfer.
type Download struct {
	ID              int        `json:"id"`
	UserID          int        `json:"userId"`
	InfoHash        string     `json:"infoHash"`
	FileIndex       int        `json:"fileIndex"`
	FilePath        string     `json:"filePath"`
	FileSize        int64      `json:"fileSize"`
	Name            string     `json:"name"`
	Magnet          string     `json:"magnet"`
	Status          string     `json:"status"`
	BytesDownloaded int64      `json:"bytesDownloaded"`
	Progress        float64    `json:"progress"`
	StartedAt       *time.Time `json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt"`
	Error           string     `json:"error"`
	CreatedAt       time.Time  `json:"createdAt"`
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
		CREATE TABLE IF NOT EXISTS downloads (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id          INTEGER NOT NULL,
			info_hash        TEXT    NOT NULL,
			file_index       INTEGER NOT NULL,
			file_path        TEXT    NOT NULL,
			file_size        INTEGER NOT NULL,
			name             TEXT    NOT NULL,
			magnet           TEXT    NOT NULL,
			status           TEXT    NOT NULL DEFAULT 'queued',
			bytes_downloaded INTEGER NOT NULL DEFAULT 0,
			started_at       DATETIME,
			completed_at     DATETIME,
			error            TEXT    NOT NULL DEFAULT '',
			created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, info_hash, file_index)
		);
		CREATE INDEX IF NOT EXISTS idx_dl_user ON downloads(user_id);
		CREATE INDEX IF NOT EXISTS idx_dl_status ON downloads(status);
	`)
	return err
}

// Create inserts a new download row in `downloading` state (immediately
// eligible for worker pickup). If a row already exists for the
// (user, info_hash, file_index) tuple, returns it unchanged — re-queueing an
// existing download is idempotent.
func (s *Store) Create(d Download) (*Download, error) {
	if d.InfoHash == "" || d.Magnet == "" {
		return nil, errors.New("infoHash e magnet são obrigatórios")
	}
	// Try to fetch existing first — idempotent enqueue
	if existing, _ := s.GetByKey(d.UserID, d.InfoHash, d.FileIndex); existing != nil {
		// If user re-enqueued a paused/failed entry, flip it back to downloading
		if existing.Status == StatusPaused || existing.Status == StatusFailed {
			_, _ = s.db.Exec(`UPDATE downloads SET status=?, error='' WHERE id=?`, StatusDownloading, existing.ID)
			existing.Status = StatusDownloading
			existing.Error = ""
		}
		return existing, nil
	}
	res, err := s.db.Exec(`
		INSERT INTO downloads(user_id, info_hash, file_index, file_path, file_size, name, magnet, status)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, d.UserID, d.InfoHash, d.FileIndex, d.FilePath, d.FileSize, d.Name, d.Magnet, StatusDownloading)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.Get(d.UserID, int(id))
}

// Get returns one download owned by userID.
func (s *Store) Get(userID, id int) (*Download, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, info_hash, file_index, file_path, file_size, name, magnet,
		       status, bytes_downloaded,
		       COALESCE(started_at, ''), COALESCE(completed_at, ''), error, created_at
		FROM downloads WHERE id=? AND user_id=?
	`, id, userID)
	return scanRow(row)
}

// GetByKey looks up a download by its uniqueness tuple. Used by Create() to
// dedupe and by the worker when reconciling state.
func (s *Store) GetByKey(userID int, infoHash string, fileIndex int) (*Download, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, info_hash, file_index, file_path, file_size, name, magnet,
		       status, bytes_downloaded,
		       COALESCE(started_at, ''), COALESCE(completed_at, ''), error, created_at
		FROM downloads WHERE user_id=? AND info_hash=? AND file_index=?
	`, userID, infoHash, fileIndex)
	return scanRow(row)
}

// List returns all downloads for the user, newest first.
func (s *Store) List(userID int) ([]Download, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, info_hash, file_index, file_path, file_size, name, magnet,
		       status, bytes_downloaded,
		       COALESCE(started_at, ''), COALESCE(completed_at, ''), error, created_at
		FROM downloads WHERE user_id=? ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Download{}
	for rows.Next() {
		d, err := scanRows(rows)
		if err != nil {
			continue
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// ListActive returns every download in `downloading` status across all users.
// The worker uses this to schedule downloads each tick.
func (s *Store) ListActive() ([]Download, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, info_hash, file_index, file_path, file_size, name, magnet,
		       status, bytes_downloaded,
		       COALESCE(started_at, ''), COALESCE(completed_at, ''), error, created_at
		FROM downloads WHERE status=? ORDER BY id
	`, StatusDownloading)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Download{}
	for rows.Next() {
		d, err := scanRows(rows)
		if err != nil {
			continue
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// ListAll returns every download — used by the streamer to compute the
// "protected from eviction" set on startup (any non-final entry should keep
// its torrent data on disk).
func (s *Store) ListAll() ([]Download, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, info_hash, file_index, file_path, file_size, name, magnet,
		       status, bytes_downloaded,
		       COALESCE(started_at, ''), COALESCE(completed_at, ''), error, created_at
		FROM downloads ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Download{}
	for rows.Next() {
		d, err := scanRows(rows)
		if err != nil {
			continue
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// SetStatus updates the lifecycle column and clears the error message when
// transitioning back to an active state. Scoped by user_id so a row can only be
// mutated by its owner (defense-in-depth: handlers also check ownership, the
// worker passes the row's own UserID).
func (s *Store) SetStatus(userID, id int, status string) error {
	if !validStatus(status) {
		return fmt.Errorf("invalid status: %s", status)
	}
	var err error
	switch status {
	case StatusDownloading:
		_, err = s.db.Exec(`
			UPDATE downloads SET status=?, error='',
			started_at = COALESCE(started_at, CURRENT_TIMESTAMP)
			WHERE id=? AND user_id=?`, status, id, userID)
	case StatusCompleted:
		_, err = s.db.Exec(`
			UPDATE downloads SET status=?, completed_at=CURRENT_TIMESTAMP WHERE id=? AND user_id=?`,
			status, id, userID)
	default:
		_, err = s.db.Exec(`UPDATE downloads SET status=? WHERE id=? AND user_id=?`, status, id, userID)
	}
	return err
}

// SetError flips a download into `failed` with a captured error message. Scoped
// by user_id (the worker passes the row's own UserID).
func (s *Store) SetError(userID, id int, msg string) error {
	_, err := s.db.Exec(`UPDATE downloads SET status=?, error=? WHERE id=? AND user_id=?`,
		StatusFailed, msg, id, userID)
	return err
}

// UpdateProgress records the latest bytes_downloaded — called periodically
// by the worker. Errors are non-fatal; the next tick will retry. Scoped by
// user_id (worker passes the row's own UserID).
func (s *Store) UpdateProgress(userID, id int, bytes int64) error {
	_, err := s.db.Exec(`UPDATE downloads SET bytes_downloaded=? WHERE id=? AND user_id=?`, bytes, id, userID)
	return err
}

// Delete removes a download row (used for user-initiated cancel).
// Cancelling does NOT erase on-disk pieces — those are cleaned by the
// streamer cache LRU once the torrent is no longer protected.
func (s *Store) Delete(userID, id int) error {
	res, err := s.db.Exec(`DELETE FROM downloads WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("download não encontrado")
	}
	return nil
}

// ─── scanning helpers ─────────────────────────────────────────────────────

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRow(row *sql.Row) (*Download, error) {
	return scanGeneric(row)
}

func scanRows(rows *sql.Rows) (*Download, error) {
	return scanGeneric(rows)
}

func scanGeneric(r rowScanner) (*Download, error) {
	d := &Download{}
	var startedAt, completedAt, createdAt string
	err := r.Scan(
		&d.ID, &d.UserID, &d.InfoHash, &d.FileIndex, &d.FilePath, &d.FileSize,
		&d.Name, &d.Magnet, &d.Status, &d.BytesDownloaded,
		&startedAt, &completedAt, &d.Error, &createdAt,
	)
	if err != nil {
		return nil, err
	}
	if t := dbutil.ParseTime(startedAt); !t.IsZero() {
		d.StartedAt = &t
	}
	if t := dbutil.ParseTime(completedAt); !t.IsZero() {
		d.CompletedAt = &t
	}
	d.CreatedAt = dbutil.ParseTime(createdAt)
	if d.FileSize > 0 {
		d.Progress = float64(d.BytesDownloaded) / float64(d.FileSize)
		if d.Progress > 1 {
			d.Progress = 1
		}
	}
	return d, nil
}

func validStatus(s string) bool {
	switch s {
	case StatusQueued, StatusDownloading, StatusCompleted, StatusFailed, StatusPaused:
		return true
	}
	return false
}
