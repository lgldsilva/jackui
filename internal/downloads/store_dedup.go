package downloads

import (
	"errors"
	"fmt"
)

// CreateLinked records a COMPLETED download that points at a PRE-EXISTING file
// (a library file, a cloud mount, another download's file) rather than at bytes
// this row fetched — cross-torrent dedup (#23) adopting an already-present file
// instead of re-downloading it. The row is marked linked=1. Idempotent on the
// (user, info_hash, file_index) key: an existing row is converted to the
// linked/completed state instead of duplicated.
func (s *Store) CreateLinked(d Download, externalPath string, fileSize int64) (*Download, error) {
	if d.InfoHash == "" || externalPath == "" {
		return nil, errors.New("infoHash e externalPath são obrigatórios")
	}
	if d.FileIndex < 0 {
		return nil, fmt.Errorf("linked download requer file_index concreto (>=0), recebido %d", d.FileIndex)
	}
	if existing, _ := s.GetByKey(d.UserID, d.InfoHash, d.FileIndex); existing != nil {
		_, err := s.db.Exec(`
			UPDATE downloads SET status=?, file_path=?, file_size=?, bytes_downloaded=?,
				linked=1, error='', completed_at=CURRENT_TIMESTAMP
			WHERE id=?`, StatusCompleted, externalPath, fileSize, fileSize, existing.ID)
		if err != nil {
			return nil, err
		}
		return s.Get(d.UserID, existing.ID)
	}
	// PG driver has no LastInsertId → RETURNING id (mirrors createOne).
	var id int64
	err := s.db.QueryRow(`
		INSERT INTO downloads(user_id, info_hash, file_index, file_path, file_size, name, magnet,
			tracker, category, status, priority, bytes_downloaded, linked, completed_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, CURRENT_TIMESTAMP) RETURNING id`,
		d.UserID, d.InfoHash, d.FileIndex, externalPath, fileSize, d.Name, d.Magnet,
		d.Tracker, d.Category, StatusCompleted, PriorityNormal, fileSize).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.Get(d.UserID, int(id))
}

// CompletedBySize returns the user's completed single-file downloads whose
// file_size equals size — the candidate set cross-torrent dedup (#23) checks a
// new file against before downloading. Whole-torrent rows (file_index < 0) and
// unfinished rows are excluded.
func (s *Store) CompletedBySize(userID int, size int64) ([]Download, error) {
	if s == nil || size <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(dlSelect+"WHERE user_id=? AND status=? AND file_path != '' AND file_index >= 0 AND file_size=?",
		userID, StatusCompleted, size)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlice(rows)
}
