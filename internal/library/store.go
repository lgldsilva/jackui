// Package library persists per-user metadata about every torrent the user has streamed.
// Stores the magnet URI so we can re-play from disk-cached pieces after restart,
// and tracks resume position so the player can seek to where the user left off.
package library

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/lgldsilva/jackui/internal/dbutil"
)

const sqlAndUserID = " AND user_id = ?"

// UpsertInput groups the parameters for Upsert.
type UpsertInput struct {
	UserID      int
	InfoHash    string
	Magnet      string
	Name        string
	PrimaryFile int
	TotalSize   int64
	Kind        string
	Incognito   bool
}

// Entry is one row in the library — a torrent the user has touched.
type Entry struct {
	ID               int    `json:"id"`
	UserID           int    `json:"userId"`
	InfoHash         string `json:"infoHash"`
	Magnet           string `json:"magnet"`
	Name             string `json:"name"`
	PrimaryFileIndex int    `json:"primaryFileIndex"`
	// LastFileIndex is the file the user actually last watched (for multi-file
	// torrents / season packs). -1 = never tracked → fall back to primary.
	LastFileIndex   int       `json:"lastFileIndex"`
	TotalSize       int64     `json:"totalSize"`
	ResumeSeconds   float64   `json:"resumeSeconds"`
	DurationSeconds float64   `json:"durationSeconds"`
	Kind            string    `json:"kind"` // "video" | "audio" | ""
	LastPlayedAt    time.Time `json:"lastPlayedAt"`
	AddedAt         time.Time `json:"addedAt"`
}

type Store struct {
	db *dbutil.DB
}

// New wires the library store onto the shared Postgres pool. Schema is applied
// centrally (internal/db migrations).
func New(pool *sql.DB) (*Store, error) {
	return &Store{db: dbutil.Wrap(pool)}, nil
}

// Close is a no-op: the shared pool's lifecycle is owned by main.
func (s *Store) Close() {
	// No-op: shared Postgres pool lifecycle is owned by main (S1186).
}

// DismissRecommendation records that the user wants to never see this recommended
// title again. Idempotent: re-dismissing the same (kind, tmdb_id) is a no-op
// (PRIMARY KEY conflict is ignored). Scoped per user. kind is "movie" | "tv".
func (s *Store) DismissRecommendation(userID int, kind string, tmdbID int) error {
	if s == nil {
		return nil
	}
	if kind == "" || tmdbID <= 0 {
		return errors.New("kind and tmdbId are required")
	}
	_, err := s.db.Exec(`
		INSERT INTO rec_dismissed(user_id, kind, tmdb_id)
		VALUES(?, ?, ?)
		ON CONFLICT(user_id, kind, tmdb_id) DO NOTHING
	`, userID, kind, tmdbID)
	return err
}

// DismissedRecommendations returns the user's dismissed recommendations as a set
// keyed by "kind:tmdbID" (e.g. "movie:603"). Used by the generator to exclude
// them from both the seed and the result list. Empty map when none / nil store.
func (s *Store) DismissedRecommendations(userID int) (map[string]bool, error) {
	set := map[string]bool{}
	if s == nil {
		return set, nil
	}
	rows, err := s.db.Query(`SELECT kind, tmdb_id FROM rec_dismissed WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind string
		var id int
		if rows.Scan(&kind, &id) == nil && kind != "" && id > 0 {
			set[DismissKey(kind, id)] = true
		}
	}
	return set, rows.Err()
}

// DismissKey builds the canonical set key for a dismissed recommendation so the
// store and the generator agree on the identity (kind + tmdbID).
func DismissKey(kind string, tmdbID int) string {
	return kind + ":" + strconv.Itoa(tmdbID)
}

// Upsert inserts a fresh row OR updates an existing (user, info_hash) tuple,
// refreshing last_played_at to now. Used on every /stream/add call.
// When incognito=true the row is written with incognito=1 so it is excluded from
// normal queries and deleted when the user ends their incognito session.
func (s *Store) Upsert(in UpsertInput) (*Entry, error) {
	if in.InfoHash == "" || in.Magnet == "" {
		return nil, errors.New("info_hash and magnet are required")
	}
	incognitoVal := 0
	if in.Incognito {
		incognitoVal = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO library(user_id, info_hash, magnet, name, primary_file_index, total_size, kind, incognito, last_played_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id, info_hash) DO UPDATE SET
			magnet             = excluded.magnet,
			name               = excluded.name,
			primary_file_index = excluded.primary_file_index,
			total_size         = excluded.total_size,
			kind               = CASE WHEN excluded.kind != '' THEN excluded.kind ELSE library.kind END,
			incognito          = excluded.incognito,
			last_played_at     = CURRENT_TIMESTAMP
	`, in.UserID, in.InfoHash, in.Magnet, in.Name, in.PrimaryFile, in.TotalSize, in.Kind, incognitoVal)
	if err != nil {
		return nil, err
	}
	return s.GetByHash(in.UserID, in.InfoHash)
}

// DeleteIncognito removes all incognito-flagged entries for a user.
// Called when the user ends their incognito session (logout or toggle-off).
func (s *Store) DeleteIncognito(userID int) error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM library WHERE user_id = ? AND incognito = 1`, userID)
	return err
}

// DeleteAllIncognito removes every incognito-flagged entry across all users.
// Called once at startup: after a restart the in-memory heartbeat map (which
// the reaper relies on) is empty, so any incognito row left in the DB is
// orphaned and would persist forever — defeating the purpose of incognito.
func (s *Store) DeleteAllIncognito() error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM library WHERE incognito = 1`)
	return err
}

// GetByHash returns the user's library entry for a given info_hash (if any).
// Incognito entries are returned regardless — callers need the entry ID for resume updates.
func (s *Store) GetByHash(userID int, infoHash string) (*Entry, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, info_hash, magnet, name, primary_file_index, last_file_index, total_size,
		       resume_seconds, duration_seconds, kind, last_played_at, added_at
		FROM library WHERE user_id = ? AND info_hash = ?
	`, userID, infoHash)
	return scanEntry(row)
}

// GetByHashPublic is like GetByHash but filters out incognito entries.
// Used by endpoints that expose data to the user's UI.
func (s *Store) GetByHashPublic(userID int, infoHash string) (*Entry, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, info_hash, magnet, name, primary_file_index, last_file_index, total_size,
		       resume_seconds, duration_seconds, kind, last_played_at, added_at
		FROM library WHERE user_id = ? AND info_hash = ? AND incognito = 0
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
		q += sqlAndUserID
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
		FROM library
		WHERE incognito = 0`
	args := []any{}
	if !includeAll {
		q += " AND user_id = ?"
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
			// #nosec G104 -- Close best-effort no cleanup; erro no teardown irrelevante
			rows.Close()
			return 0, err
		}
		todo = append(todo, st)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	// #nosec G104 -- Close best-effort no cleanup; erro no teardown irrelevante
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
		q += sqlAndUserID
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
		q += sqlAndUserID
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
	err := row.Scan(
		&e.ID, &e.UserID, &e.InfoHash, &e.Magnet, &e.Name,
		&e.PrimaryFileIndex, &e.LastFileIndex, &e.TotalSize,
		&e.ResumeSeconds, &e.DurationSeconds, &e.Kind,
		&e.LastPlayedAt, &e.AddedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// scanEntryRows reuses scanEntry on a rows iterator (separate impl because *sql.Row vs *sql.Rows).
func scanEntryRows(rows *sql.Rows) (*Entry, error) {
	var e Entry
	err := rows.Scan(
		&e.ID, &e.UserID, &e.InfoHash, &e.Magnet, &e.Name,
		&e.PrimaryFileIndex, &e.LastFileIndex, &e.TotalSize,
		&e.ResumeSeconds, &e.DurationSeconds, &e.Kind,
		&e.LastPlayedAt, &e.AddedAt,
	)
	if err != nil {
		return nil, err
	}
	return &e, nil
}
