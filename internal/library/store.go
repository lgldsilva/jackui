// Package library persists per-user metadata about every torrent the user has streamed.
// Stores the magnet URI so we can re-play from disk-cached pieces after restart,
// and tracks resume position so the player can seek to where the user left off.
package library

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luizg/jackui/internal/dbutil"
	_ "modernc.org/sqlite"
)

// Entry is one row in the library — a torrent the user has touched.
type Entry struct {
	ID               int       `json:"id"`
	UserID           int       `json:"userId"`
	InfoHash         string    `json:"infoHash"`
	Magnet           string    `json:"magnet"`
	Name             string    `json:"name"`
	PrimaryFileIndex int       `json:"primaryFileIndex"`
	// LastFileIndex is the file the user actually last watched (for multi-file
	// torrents / season packs). -1 = never tracked → fall back to primary.
	LastFileIndex int       `json:"lastFileIndex"`
	TotalSize     int64     `json:"totalSize"`
	ResumeSeconds float64   `json:"resumeSeconds"`
	DurationSeconds  float64   `json:"durationSeconds"`
	Kind             string    `json:"kind"` // "video" | "audio" | ""
	LastPlayedAt     time.Time `json:"lastPlayedAt"`
	AddedAt          time.Time `json:"addedAt"`
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
		CREATE TABLE IF NOT EXISTS library (
			id                  INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id             INTEGER NOT NULL,
			info_hash           TEXT    NOT NULL,
			magnet              TEXT    NOT NULL,
			name                TEXT    NOT NULL,
			primary_file_index  INTEGER NOT NULL DEFAULT 0,
			total_size          INTEGER NOT NULL DEFAULT 0,
			resume_seconds      REAL    NOT NULL DEFAULT 0,
			duration_seconds    REAL    NOT NULL DEFAULT 0,
			kind                TEXT    NOT NULL DEFAULT '',
			last_played_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			added_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, info_hash)
		);
		CREATE INDEX IF NOT EXISTS idx_lib_user_played ON library(user_id, last_played_at DESC);
		CREATE INDEX IF NOT EXISTS idx_lib_hash        ON library(info_hash);
	`)
	if err != nil {
		return err
	}
	// Added later: track the actually-watched file (-1 = unknown). Idempotent —
	// SQLite has no "ADD COLUMN IF NOT EXISTS", so ignore the duplicate error.
	if _, aerr := s.db.Exec(`ALTER TABLE library ADD COLUMN last_file_index INTEGER NOT NULL DEFAULT -1`); aerr != nil &&
		!strings.Contains(aerr.Error(), "duplicate column") {
		return aerr
	}
	return nil
}

// Upsert inserts a fresh row OR updates an existing (user, info_hash) tuple,
// refreshing last_played_at to now. Used on every /stream/add call.
func (s *Store) Upsert(userID int, infoHash, magnet, name string, primaryFile int, totalSize int64, kind string) (*Entry, error) {
	if infoHash == "" || magnet == "" {
		return nil, errors.New("info_hash and magnet are required")
	}
	_, err := s.db.Exec(`
		INSERT INTO library(user_id, info_hash, magnet, name, primary_file_index, total_size, kind, last_played_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id, info_hash) DO UPDATE SET
			magnet             = excluded.magnet,
			name               = excluded.name,
			primary_file_index = excluded.primary_file_index,
			total_size         = excluded.total_size,
			kind               = CASE WHEN excluded.kind != '' THEN excluded.kind ELSE library.kind END,
			last_played_at     = CURRENT_TIMESTAMP
	`, userID, infoHash, magnet, name, primaryFile, totalSize, kind)
	if err != nil {
		return nil, err
	}
	return s.GetByHash(userID, infoHash)
}

// GetByHash returns the user's library entry for a given info_hash (if any).
func (s *Store) GetByHash(userID int, infoHash string) (*Entry, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, info_hash, magnet, name, primary_file_index, last_file_index, total_size,
		       resume_seconds, duration_seconds, kind, last_played_at, added_at
		FROM library WHERE user_id = ? AND info_hash = ?
	`, userID, infoHash)
	return scanEntry(row)
}

// GetByID returns one entry, optionally bypassing user check (admin).
func (s *Store) GetByID(id int, userID int, includeAll bool) (*Entry, error) {
	q := `
		SELECT id, user_id, info_hash, magnet, name, primary_file_index, last_file_index, total_size,
		       resume_seconds, duration_seconds, kind, last_played_at, added_at
		FROM library WHERE id = ?`
	args := []any{id}
	if !includeAll {
		q += " AND user_id = ?"
		args = append(args, userID)
	}
	return scanEntry(s.db.QueryRow(q, args...))
}

// List returns the user's library, ordered by most recently played first.
// includeAll lets admin see everyone.
func (s *Store) List(userID int, includeAll bool, limit int) ([]Entry, error) {
	q := `
		SELECT id, user_id, info_hash, magnet, name, primary_file_index, last_file_index, total_size,
		       resume_seconds, duration_seconds, kind, last_played_at, added_at
		FROM library`
	args := []any{}
	if !includeAll {
		q += " WHERE user_id = ?"
		args = append(args, userID)
	}
	q += " ORDER BY last_played_at DESC"
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e, err := scanEntryRows(rows)
		if err != nil {
			continue
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// PrimaryFileLookup is the contract a one-shot migration needs from any
// metadata source: given an info_hash, return the primary file index (or
// false when no snapshot exists).
type PrimaryFileLookup func(infoHash string) (primaryFile int, ok bool)

// RefreshStalePrimary scans library rows whose primary_file_index is still 0
// (the column default — almost always means "never had a real pickPrimaryFile
// computed against the latest code") and refreshes them from the supplied
// lookup. Returns the number of rows updated.
//
// Why this exists: before pickPrimaryFile learned to skip featurettes/extras,
// every series-pack entry got primary_file_index=0 — a file that's commonly
// "Featurettes/.../Behind The Scenes.mkv" sorted to the start of the torrent.
// That stale 0 then leaked through LibraryPage into the player, which
// happily streamed the wrong file. A one-shot startup migration cures all
// pre-fix rows without forcing the user to re-add anything.
//
// Only positive lookups (primaryFile > 0) update anything — keeps audio-only
// torrents where pickPrimaryFile returns -1 untouched, since 0 may be the
// correct "first track" choice for those.
func (s *Store) RefreshStalePrimary(lookup PrimaryFileLookup) (int, error) {
	rows, err := s.db.Query(`SELECT id, info_hash FROM library WHERE primary_file_index = 0`)
	if err != nil {
		return 0, err
	}
	type stale struct {
		id   int
		hash string
	}
	var todo []stale
	for rows.Next() {
		var st stale
		if err := rows.Scan(&st.id, &st.hash); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, st)
	}
	rows.Close()
	updated := 0
	for _, st := range todo {
		pf, ok := lookup(st.hash)
		if !ok || pf <= 0 {
			continue
		}
		if _, err := s.db.Exec(`UPDATE library SET primary_file_index = ? WHERE id = ?`, pf, st.id); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

// UpdateResume saves the current playback position. Called periodically by the player.
// Optionally updates duration if not yet known.
func (s *Store) UpdateResume(id, userID int, resumeSeconds, durationSeconds float64, fileIndex int, includeAll bool) error {
	q := `UPDATE library SET resume_seconds = ?, last_played_at = CURRENT_TIMESTAMP`
	args := []any{resumeSeconds}
	if durationSeconds > 0 {
		q += `, duration_seconds = ?`
		args = append(args, durationSeconds)
	}
	// Track the actually-watched file so reopening a multi-file torrent resumes
	// the same episode. -1 means the caller didn't know it — leave it untouched.
	if fileIndex >= 0 {
		q += `, last_file_index = ?`
		args = append(args, fileIndex)
	}
	q += " WHERE id = ?"
	args = append(args, id)
	if !includeAll {
		q += " AND user_id = ?"
		args = append(args, userID)
	}
	_, err := s.db.Exec(q, args...)
	return err
}

// Delete removes an entry. Returns an error if the entry isn't owned by user (unless admin).
func (s *Store) Delete(id, userID int, includeAll bool) error {
	q := `DELETE FROM library WHERE id = ?`
	args := []any{id}
	if !includeAll {
		q += " AND user_id = ?"
		args = append(args, userID)
	}
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("entry not found or not owned")
	}
	return nil
}

// DeleteAll removes every library entry owned by the user. Admins can pass
// includeAll=true to wipe the entire history across users (rarely useful — meant
// mostly for support / DB resets).
func (s *Store) DeleteAll(userID int, includeAll bool) (int64, error) {
	q := `DELETE FROM library`
	args := []any{}
	if !includeAll {
		q += " WHERE user_id = ?"
		args = append(args, userID)
	}
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// scanEntry handles a single row from QueryRow.
func scanEntry(row interface{ Scan(...any) error }) (*Entry, error) {
	var e Entry
	var lastPlayed, added string
	err := row.Scan(
		&e.ID, &e.UserID, &e.InfoHash, &e.Magnet, &e.Name,
		&e.PrimaryFileIndex, &e.LastFileIndex, &e.TotalSize,
		&e.ResumeSeconds, &e.DurationSeconds, &e.Kind,
		&lastPlayed, &added,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.LastPlayedAt= dbutil.ParseTime(lastPlayed)
	e.AddedAt= dbutil.ParseTime(added)
	return &e, nil
}

// scanEntryRows reuses scanEntry on a rows iterator (separate impl because *sql.Row vs *sql.Rows).
func scanEntryRows(rows *sql.Rows) (*Entry, error) {
	var e Entry
	var lastPlayed, added string
	err := rows.Scan(
		&e.ID, &e.UserID, &e.InfoHash, &e.Magnet, &e.Name,
		&e.PrimaryFileIndex, &e.LastFileIndex, &e.TotalSize,
		&e.ResumeSeconds, &e.DurationSeconds, &e.Kind,
		&lastPlayed, &added,
	)
	if err != nil {
		return nil, err
	}
	e.LastPlayedAt= dbutil.ParseTime(lastPlayed)
	e.AddedAt= dbutil.ParseTime(added)
	return &e, nil
}
