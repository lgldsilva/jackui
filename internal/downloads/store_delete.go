package downloads

import (
	"database/sql"
	"errors"
)

// Delete removes a download row (used for user-initiated cancel).
// Cancelling does NOT erase on-disk pieces — those are cleaned by the
// streamer cache LRU once the torrent is no longer protected.
//
// IDEMPOTENT: a row that is already gone returns nil, not an error. The
// previous "download não encontrado" error turned every double-delete (and
// every admin delete of another user's row before DeleteScoped existed) into a
// 500 the frontend swallowed silently — the 2s poll then re-showed the row, so
// the user saw "clicked Remove, nothing happened". The DELETE is now
// authoritative: once it returns, the row does not exist, whether we removed it
// now or it was already gone.
func (s *Store) Delete(userID, id int) error {
	_, err := s.deleteScoped(id, userID, false)
	return err
}

// DeleteScoped removes a download row, returning the row as it was BEFORE the
// delete (so the caller can drop the torrent / notify the worker by infoHash)
// or nil when no matching row existed. Idempotent: a missing row is not an
// error. When isAdmin is true the delete is NOT scoped to userID — an admin in
// the "all users" view (DownloadsListAll returns rows of every user) can remove
// any row. Without this, store.Delete(adminID, otherUsersRowID) matched 0 rows
// and the row survived — the confirmed root cause of the intermittent
// "sometimes Remove doesn't remove" (it depended on whose row you clicked).
func (s *Store) DeleteScoped(userID, id int, isAdmin bool) (*Download, error) {
	return s.deleteScoped(id, userID, isAdmin)
}

func (s *Store) deleteScoped(id, userID int, isAdmin bool) (*Download, error) {
	row, err := s.lookupForDelete(id, userID, isAdmin)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil // already gone — idempotent no-op
	}
	q := `DELETE FROM downloads WHERE id=?`
	args := []any{id}
	if !isAdmin {
		q += ` AND user_id=?`
		args = append(args, userID)
	}
	if _, err := s.db.Exec(q, args...); err != nil {
		return nil, err
	}
	return row, nil
}

// lookupForDelete fetches the row a delete is about to remove, honoring the
// same ownership scope as the delete itself. Returns (nil, nil) when no row
// matches — keeping delete idempotent.
func (s *Store) lookupForDelete(id, userID int, isAdmin bool) (*Download, error) {
	q := dlSelect + "WHERE id=?"
	args := []any{id}
	if !isAdmin {
		q += " AND user_id=?"
		args = append(args, userID)
	}
	d, err := scanRow(s.db.QueryRow(q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}
