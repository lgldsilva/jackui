package downloads

import (
	"fmt"
)

// Setters/updates de campos do store — extraído de store.go.
// SetStatus updates the lifecycle column and clears the error message when
// transitioning back to an active state. Scoped by user_id so a row can only be
// mutated by its owner (defense-in-depth: handlers also check ownership, the
// worker passes the row's own UserID).
func (s *Store) SetStatus(userID, id int, status string) error {
	if !validStatus(status) {
		return fmt.Errorf(errInvalidStatus, status)
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

// SetStatusForUser updates status for ALL non-terminal rows owned by userID.
// Terminal statuses (completed, failed) are left unchanged.
func (s *Store) SetStatusForUser(userID int, status string) (int64, error) {
	if !validStatus(status) {
		return 0, fmt.Errorf(errInvalidStatus, status)
	}
	res, err := s.db.Exec(`UPDATE downloads SET status=? WHERE user_id=? AND status NOT IN (?, ?)`,
		status, userID, StatusCompleted, StatusFailed)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetStatusByIDs updates status for specific download IDs owned by userID.
func (s *Store) SetStatusByIDs(userID int, ids []int, status string) (int64, error) {
	if !validStatus(status) {
		return 0, fmt.Errorf(errInvalidStatus, status)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	q := "UPDATE downloads SET status=? WHERE user_id=? AND id IN ("
	args := []any{status, userID}
	for i, id := range ids {
		if i > 0 {
			q += ","
		}
		q += "?"
		args = append(args, id)
	}
	q += ")"
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetError flips a download into `failed` with a captured error message. Scoped
// by user_id (the worker passes the row's own UserID).
func (s *Store) SetError(userID, id int, msg string) error {
	_, err := s.db.Exec(`UPDATE downloads SET status=?, error=? WHERE id=? AND user_id=?`,
		StatusFailed, msg, id, userID)
	return err
}

// SetActiveMagnet persists an alternative active source magnet (EffectiveMagnet
// then prefers it). Used when the original source — typically an ephemeral
// indexer .torrent URL — dies (404) and the worker falls back to a bare
// info_hash magnet, so later retries/reboots skip the dead URL. Scoped by user_id.
func (s *Store) SetActiveMagnet(userID, id int, magnet string) error {
	_, err := s.db.Exec(`UPDATE downloads SET active_magnet=? WHERE id=? AND user_id=?`, magnet, id, userID)
	return err
}

// SetFilePath updates the on-disk path after the worker moves a completed file
// to the download directory. Scoped by user_id (worker passes the row's own UserID).
func (s *Store) SetFilePath(userID, id int, path string) error {
	_, err := s.db.Exec(`UPDATE downloads SET file_path=? WHERE id=? AND user_id=?`, path, id, userID)
	return err
}

// UpdateName records the actual torrent folder name resolved from metadata.
// The row is created with the search-result title, but the real torrent name
// (t.Name()) is what the streamer registers for eviction protection — they
// often differ. Persisting the real name keeps the boot-time RegisterDownload
// in NewWorker consistent so a restart doesn't protect the wrong path.
func (s *Store) UpdateName(userID, id int, name string) error {
	_, err := s.db.Exec(`UPDATE downloads SET name=? WHERE id=? AND user_id=?`, name, id, userID)
	return err
}

// SetCategory updates the download's category/label. Used pela Transmission RPC
// (torrent-set "labels"). Scoped by user_id.
func (s *Store) SetCategory(userID, id int, category string) error {
	_, err := s.db.Exec(`UPDATE downloads SET category=? WHERE id=? AND user_id=?`, category, id, userID)
	return err
}

// UpdateMetadata updates the resolved torrent name, file path inside the torrent, and file size in bytes.
// Called by the background worker once torrent metadata is fully resolved.
func (s *Store) UpdateMetadata(userID, id int, name string, filePath string, fileSize int64) error {
	_, err := s.db.Exec(`UPDATE downloads SET name=?, file_path=?, file_size=? WHERE id=? AND user_id=?`, name, filePath, fileSize, id, userID)
	return err
}

// SetCompletionDest freezes the per-torrent destination dir resolved once metadata
// is known, so the completion finalize uses a stable path even if completionBaseDir's
// inputs (category, auto-promote) change later. Non-fatal (next finalize falls back).
func (s *Store) SetCompletionDest(userID, id int, dest string) error {
	_, err := s.db.Exec(`UPDATE downloads SET completion_dest=? WHERE id=? AND user_id=?`, dest, id, userID)
	return err
}

// UpdateProgress records the latest bytes_downloaded — called periodically
// by the worker. Errors are non-fatal; the next tick will retry. Scoped by
// user_id (worker passes the row's own UserID).
func (s *Store) UpdateProgress(userID, id int, bytes int64) error {
	_, err := s.db.Exec(`UPDATE downloads SET bytes_downloaded=? WHERE id=? AND user_id=?`, bytes, id, userID)
	return err
}

// ProgressUpdate is one row's freshly-sampled byte count, batched by the worker.
type ProgressUpdate struct {
	UserID int
	ID     int
	Bytes  int64
}

// UpdateProgressBatch writes the per-file progress of an entire torrent group in
// ONE transaction. The aggregate-by-torrent tick samples the live *torrent.Torrent
// once and records each selected file's BytesCompleted on its own row, so a
// 389-file pack costs one tx instead of 389 separate UPDATEs (the I/O the OOM fix
// targets). With MaxOpenConns(1) the single tx also serializes cleanly. A nil/
// empty batch is a no-op.
func (s *Store) UpdateProgressBatch(items []ProgressUpdate) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`UPDATE downloads SET bytes_downloaded=? WHERE id=? AND user_id=?`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, it := range items {
		if _, err := stmt.Exec(it.Bytes, it.ID, it.UserID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
