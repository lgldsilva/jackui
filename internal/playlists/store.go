// Package playlists manages per-user ordered collections of torrent references.
// A playlist contains items that reference a magnet + info_hash; resume state
// lives in the library package so the same content played from search vs.
// playlist shares its watched position.
package playlists

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lgldsilva/jackui/internal/dbutil"
)

const (
	sqlAndUserID        = " AND user_id = ?"
	errPlaylistNotFound = "playlist not found or not owned"
)

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
func (s *Store) Close() {}

// ─── Playlists ─────────────────────────────────────────────────────────────

// Create makes a new empty playlist owned by userID.
func (s *Store) Create(userID int, name, description string) (*Playlist, error) {
	if name == "" {
		return nil, errors.New("name is required")
	}
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO playlists(user_id, name, description) VALUES(?, ?, ?) RETURNING id`,
		userID, name, description,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.Get(int(id), userID, false)
}

// List returns the user's playlists (or all when includeAll=true), with item counts.
func (s *Store) List(userID int, includeAll bool) ([]Playlist, error) {
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
	rows, err := s.db.Query(q, args...)
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
func (s *Store) Get(id, userID int, includeAll bool) (*Playlist, error) {
	q := `
		SELECT id, user_id, name, description, created_at, updated_at
		FROM playlists WHERE id = ?`
	args := []any{id}
	if !includeAll {
		q += sqlAndUserID
		args = append(args, userID)
	}
	var p Playlist
	err := s.db.QueryRow(q, args...).Scan(&p.ID, &p.UserID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Update renames / re-describes a playlist.
func (s *Store) Update(id, userID int, name, description string, includeAll bool) error {
	q := `UPDATE playlists SET name = ?, description = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	args := []any{name, description, id}
	if !includeAll {
		q += sqlAndUserID
		args = append(args, userID)
	}
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf(errPlaylistNotFound)
	}
	return nil
}

// Delete drops a playlist (and cascades to items).
func (s *Store) Delete(id, userID int, includeAll bool) error {
	q := `DELETE FROM playlists WHERE id = ?`
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
		return fmt.Errorf(errPlaylistNotFound)
	}
	return nil
}

// ─── Items ─────────────────────────────────────────────────────────────────

// AddItem appends an item to the end of the playlist.
func (s *Store) AddItem(playlistID, userID int, item Item, includeAll bool) (*Item, error) {
	if !s.ownsPlaylist(playlistID, userID, includeAll) {
		return nil, fmt.Errorf(errPlaylistNotFound)
	}
	if item.Magnet == "" || item.Title == "" {
		return nil, errors.New("magnet and title required")
	}
	// Determine next position
	var maxPos sql.NullInt64
	_ = s.db.QueryRow(`SELECT MAX(position) FROM playlist_items WHERE playlist_id = ?`, playlistID).Scan(&maxPos)
	nextPos := 0
	if maxPos.Valid {
		nextPos = int(maxPos.Int64) + 1
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var libraryIDArg any
	if item.LibraryID != nil {
		libraryIDArg = *item.LibraryID
	}

	var id int64
	err = tx.QueryRow(
		`INSERT INTO playlist_items(playlist_id, position, library_id, title, magnet, info_hash, file_index)
		 VALUES(?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		playlistID, nextPos, libraryIDArg, item.Title, item.Magnet, item.InfoHash, item.FileIndex,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(`UPDATE playlists SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, playlistID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.getItem(int(id))
}

// Items returns all items in a playlist, ordered by position.
func (s *Store) Items(playlistID, userID int, includeAll bool) ([]Item, error) {
	if !s.ownsPlaylist(playlistID, userID, includeAll) {
		return nil, fmt.Errorf(errPlaylistNotFound)
	}
	rows, err := s.db.Query(`
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
func (s *Store) RemoveItem(playlistID, itemID, userID int, includeAll bool) error {
	if !s.ownsPlaylist(playlistID, userID, includeAll) {
		return fmt.Errorf(errPlaylistNotFound)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var pos int
	if err := tx.QueryRow(`SELECT position FROM playlist_items WHERE id = ? AND playlist_id = ?`,
		itemID, playlistID).Scan(&pos); err != nil {
		return fmt.Errorf("item not found")
	}
	if _, err := tx.Exec(`DELETE FROM playlist_items WHERE id = ?`, itemID); err != nil {
		return err
	}
	// Compact positions after the gap
	if _, err := tx.Exec(`UPDATE playlist_items SET position = position - 1 WHERE playlist_id = ? AND position > ?`,
		playlistID, pos); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE playlists SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, playlistID); err != nil {
		return err
	}
	return tx.Commit()
}

// Reorder moves an item to a new position, shifting siblings as needed.
func (s *Store) Reorder(playlistID, itemID, userID, newPos int, includeAll bool) error {
	if !s.ownsPlaylist(playlistID, userID, includeAll) {
		return fmt.Errorf(errPlaylistNotFound)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var curPos int
	if err := tx.QueryRow(`SELECT position FROM playlist_items WHERE id = ? AND playlist_id = ?`,
		itemID, playlistID).Scan(&curPos); err != nil {
		return fmt.Errorf("item not found")
	}
	if curPos == newPos {
		return nil
	}
	if newPos > curPos {
		// Shift items between (curPos, newPos] down by 1
		_, _ = tx.Exec(`UPDATE playlist_items SET position = position - 1
		         WHERE playlist_id = ? AND position > ? AND position <= ?`, playlistID, curPos, newPos)
	} else {
		// Shift items between [newPos, curPos) up by 1
		_, _ = tx.Exec(`UPDATE playlist_items SET position = position + 1
		         WHERE playlist_id = ? AND position >= ? AND position < ?`, playlistID, newPos, curPos)
	}
	_, _ = tx.Exec(`UPDATE playlist_items SET position = ? WHERE id = ?`, newPos, itemID)
	_, _ = tx.Exec(`UPDATE playlists SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, playlistID)
	return tx.Commit()
}

// ─── helpers ───────────────────────────────────────────────────────────────

func (s *Store) ownsPlaylist(playlistID, userID int, includeAll bool) bool {
	if includeAll {
		var n int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM playlists WHERE id = ?`, playlistID).Scan(&n)
		return n > 0
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM playlists WHERE id = ? AND user_id = ?`, playlistID, userID).Scan(&n)
	return n > 0
}

func (s *Store) getItem(id int) (*Item, error) {
	var it Item
	var libID sql.NullInt64
	err := s.db.QueryRow(`
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
