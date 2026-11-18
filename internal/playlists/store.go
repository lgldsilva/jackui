// Package playlists manages per-user ordered collections of torrent references.
// A playlist contains items that reference a magnet + info_hash; resume state
// lives in the library package so the same content played from search vs.
// playlist shares its watched position.
package playlists

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lgldsilva/jackui/internal/dbutil"
)

const (
	sqlAndUserID = " AND user_id = ?"
)

// ErrNotFound is returned when the playlist does not exist or is not owned by
// the caller. Handlers map it to HTTP 404 (sweep e2e 2026-11-06 §3.2.6: the
// GETs already answered 404 while the mutations leaked 500).
var ErrNotFound = errors.New("playlist not found or not owned")

// Playlist is one user-owned ordered collection.
type Playlist struct {
	ID          int       `json:"id"`
	UserID      int       `json:"userId"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	ItemCount   int       `json:"itemCount,omitempty"` // populated by List, not Get
}

// Item is one position in a playlist.
type Item struct {
	ID         int       `json:"id"`
	PlaylistID int       `json:"playlistId"`
	Position   int       `json:"position"`
	LibraryID  *int      `json:"libraryId,omitempty"`
	Title      string    `json:"title"`
	Magnet     string    `json:"magnet"`
	InfoHash   string    `json:"infoHash"`
	FileIndex  int       `json:"fileIndex"`
	AddedAt    time.Time `json:"addedAt"`
}

type Store struct {
	db *dbutil.DB
}

// New wires the playlists store onto the shared Postgres pool. Schema is applied
// centrally (internal/db migrations).
func New(pool *sql.DB) (*Store, error) {
	return &Store{db: dbutil.Wrap(pool)}, nil
}

// Close is a no-op: the shared pool's lifecycle is owned by main.

// ─── Playlists ─────────────────────────────────────────────────────────────

// Create makes a new empty playlist owned by userID.
func (s *Store) Create(ctx context.Context, userID int, name, description string) (*Playlist, error) {
	if name == "" {
		return nil, errors.New("name is required")
	}
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO playlists(user_id, name, description) VALUES(?, ?, ?) RETURNING id`,
		userID, name, description,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, int(id), userID, false)
}

// List returns the user's playlists (or all when includeAll=true), with item counts.
func (s *Store) List(ctx context.Context, userID int, includeAll bool) ([]Playlist, error) {
	q := `
		SELECT p.id, p.user_id, p.name, p.description, p.created_at, p.updated_at,
		       COALESCE((SELECT COUNT(*) FROM playlist_items WHERE playlist_id = p.id), 0)
		FROM playlists p`
	args := []any{}
	if !includeAll {
		q += " WHERE p.user_id = ?"
		args = append(args, userID)
	}
	q += " ORDER BY p.updated_at DESC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Playlist{}
	for rows.Next() {
		var p Playlist
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt, &p.ItemCount); err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get returns one playlist (without items). Returns nil if not found / not owned.
func (s *Store) Get(ctx context.Context, id, userID int, includeAll bool) (*Playlist, error) {
	q := `
		SELECT id, user_id, name, description, created_at, updated_at
		FROM playlists WHERE id = ?`
	args := []any{id}
	if !includeAll {
		q += sqlAndUserID
		args = append(args, userID)
	}
	var p Playlist
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&p.ID, &p.UserID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Update renames / re-describes a playlist.
func (s *Store) Update(ctx context.Context, id, userID int, name, description string, includeAll bool) error {
	q := `UPDATE playlists SET name = ?, description = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	args := []any{name, description, id}
	if !includeAll {
		q += sqlAndUserID
		args = append(args, userID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete drops a playlist (and cascades to items).
func (s *Store) Delete(ctx context.Context, id, userID int, includeAll bool) error {
	q := `DELETE FROM playlists WHERE id = ?`
	args := []any{id}
	if !includeAll {
		q += sqlAndUserID
		args = append(args, userID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Items ─────────────────────────────────────────────────────────────────

// AddItem appends an item to the end of the playlist.
func (s *Store) AddItem(ctx context.Context, playlistID, userID int, item Item, includeAll bool) (*Item, error) {
	owned, err := s.ownsPlaylist(ctx, playlistID, userID, includeAll)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, ErrNotFound
	}
	if item.Magnet == "" || item.Title == "" {
		return nil, errors.New("magnet and title required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Lock the playlist row so concurrent adds serialize on MAX(position)
	// (read outside the tx, two adds used to get the same position).
	if err := lockPlaylist(ctx, tx, playlistID); err != nil {
		return nil, err
	}
	var maxPos sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(position) FROM playlist_items WHERE playlist_id = ?`, playlistID).Scan(&maxPos); err != nil {
		return nil, err
	}
	nextPos := 0
	if maxPos.Valid {
		nextPos = int(maxPos.Int64) + 1
	}

	var libraryIDArg any
	if item.LibraryID != nil {
		libraryIDArg = *item.LibraryID
	}

	var id int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO playlist_items(playlist_id, position, library_id, title, magnet, info_hash, file_index)
		 VALUES(?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		playlistID, nextPos, libraryIDArg, item.Title, item.Magnet, item.InfoHash, item.FileIndex,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE playlists SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, playlistID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.getItem(ctx, int(id))
}

// Items returns all items in a playlist, ordered by position.
func (s *Store) Items(ctx context.Context, playlistID, userID int, includeAll bool) ([]Item, error) {
	owned, err := s.ownsPlaylist(ctx, playlistID, userID, includeAll)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, playlist_id, position, library_id, title, magnet, info_hash, file_index, added_at
		FROM playlist_items WHERE playlist_id = ? ORDER BY position`, playlistID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Item{}
	for rows.Next() {
		var it Item
		var libID sql.NullInt64
		if err := rows.Scan(&it.ID, &it.PlaylistID, &it.Position, &libID,
			&it.Title, &it.Magnet, &it.InfoHash, &it.FileIndex, &it.AddedAt); err != nil {
			continue
		}
		if libID.Valid {
			v := int(libID.Int64)
			it.LibraryID = &v
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// RemoveItem deletes an item and compacts positions.
func (s *Store) RemoveItem(ctx context.Context, playlistID, itemID, userID int, includeAll bool) error {
	owned, err := s.ownsPlaylist(ctx, playlistID, userID, includeAll)
	if err != nil {
		return err
	}
	if !owned {
		return ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var pos int
	if err := tx.QueryRowContext(ctx, `SELECT position FROM playlist_items WHERE id = ? AND playlist_id = ?`,
		itemID, playlistID).Scan(&pos); err != nil {
		return fmt.Errorf("item not found")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM playlist_items WHERE id = ?`, itemID); err != nil {
		return err
	}
	// Compact positions after the gap
	if _, err := tx.ExecContext(ctx, `UPDATE playlist_items SET position = position - 1 WHERE playlist_id = ? AND position > ?`,
		playlistID, pos); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE playlists SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, playlistID); err != nil {
		return err
	}
	return tx.Commit()
}

// Reorder moves an item to a new position, shifting siblings as needed.
func (s *Store) Reorder(ctx context.Context, playlistID, itemID, userID, newPos int, includeAll bool) error {
	owned, err := s.ownsPlaylist(ctx, playlistID, userID, includeAll)
	if err != nil {
		return err
	}
	if !owned {
		return ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockPlaylist(ctx, tx, playlistID); err != nil {
		return err
	}

	var curPos, count int
	if err := tx.QueryRowContext(ctx, `SELECT position FROM playlist_items WHERE id = ? AND playlist_id = ?`,
		itemID, playlistID).Scan(&curPos); err != nil {
		return fmt.Errorf("item not found")
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_items WHERE playlist_id = ?`, playlistID).Scan(&count); err != nil {
		return err
	}
	newPos = max(0, min(newPos, count-1)) // out-of-range moves go to the ends, never leave a gap
	if curPos == newPos {
		return nil
	}
	for _, st := range reorderStatements(playlistID, itemID, curPos, newPos) {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type sqlStmt struct {
	q    string
	args []any
}

// reorderStatements shifts the items between the old and new position by one
// and places the moved item, then bumps the playlist's updated_at.
func reorderStatements(playlistID, itemID, curPos, newPos int) []sqlStmt {
	shift := sqlStmt{`UPDATE playlist_items SET position = position - 1
		WHERE playlist_id = ? AND position > ? AND position <= ?`, []any{playlistID, curPos, newPos}}
	if newPos < curPos {
		shift = sqlStmt{`UPDATE playlist_items SET position = position + 1
			WHERE playlist_id = ? AND position >= ? AND position < ?`, []any{playlistID, newPos, curPos}}
	}
	return []sqlStmt{
		shift,
		{`UPDATE playlist_items SET position = ? WHERE id = ?`, []any{newPos, itemID}},
		{`UPDATE playlists SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, []any{playlistID}},
	}
}

// lockPlaylist takes a row lock on the playlist for the rest of tx, serializing
// position changes (add/reorder) on the same playlist.
func lockPlaylist(ctx context.Context, tx *dbutil.Tx, playlistID int) error {
	var id int
	return tx.QueryRowContext(ctx, `SELECT id FROM playlists WHERE id = ? FOR UPDATE`, playlistID).Scan(&id)
}

// ─── helpers ───────────────────────────────────────────────────────────────

// ownsPlaylist reports whether the playlist exists (and is owned, unless the
// caller is an admin). Infrastructure failures are returned as an error so the
// handlers can answer 500 instead of mistaking a dead database for a missing
// playlist (sonar_error_coverage pool-close contract).
func (s *Store) ownsPlaylist(ctx context.Context, playlistID, userID int, includeAll bool) (bool, error) {
	var n int
	if includeAll {
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlists WHERE id = ?`, playlistID).Scan(&n)
		if err != nil {
			return false, err
		}
		return n > 0, nil
	}
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlists WHERE id = ? AND user_id = ?`, playlistID, userID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) getItem(ctx context.Context, id int) (*Item, error) {
	var it Item
	var libID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, playlist_id, position, library_id, title, magnet, info_hash, file_index, added_at
		FROM playlist_items WHERE id = ?`, id).Scan(
		&it.ID, &it.PlaylistID, &it.Position, &libID, &it.Title, &it.Magnet, &it.InfoHash, &it.FileIndex, &it.AddedAt,
	)
	if err != nil {
		return nil, err
	}
	if libID.Valid {
		v := int(libID.Int64)
		it.LibraryID = &v
	}
	return &it, nil
}
