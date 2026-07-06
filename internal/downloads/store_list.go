package downloads

import (
	"database/sql"
)

// ListMaxResults caps list endpoints so a multi-file pack cannot return unbounded
// rows to the UI poll (778-file torrent = 778 JSON objects every 2s).
const ListMaxResults = 5000

const listLimitClause = " LIMIT 5000"

// Leituras/listagens do store de downloads — extraído de store.go.
// List returns all downloads for the user, newest first.
func (s *Store) List(userID int) ([]Download, error) {
	rows, err := s.db.Query(dlSelect+"WHERE user_id=? ORDER BY created_at DESC"+listLimitClause, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlice(rows)
}

// ListFiltered returns downloads for the user filtered by optional criteria.
// Empty filters are ignored. Sort is "created_at" (default), "name", "size", "progress".
func (s *Store) ListFiltered(f ListFilter) ([]Download, error) {
	q := "WHERE user_id=?"
	args := []any{f.UserID}
	if f.Status != "" {
		q += " AND status=?"
		args = append(args, f.Status)
	}
	if f.Tracker != "" {
		q += " AND tracker=?"
		args = append(args, f.Tracker)
	}
	if f.Category != "" {
		q += " AND category=?"
		args = append(args, f.Category)
	}
	if f.Search != "" {
		q += " AND (name LIKE ? OR file_path LIKE ?)"
		s := "%" + f.Search + "%"
		args = append(args, s, s)
	}
	order := "created_at"
	switch f.SortCol {
	case "name":
		order = "name"
	case "size":
		order = "file_size"
	case "progress":
		order = "bytes_downloaded"
	case "status":
		order = "status"
	case "tracker":
		order = "tracker"
	case "category":
		order = "category"
	}
	dir := "DESC"
	if f.SortDir == "asc" {
		dir = "ASC"
	}
	rows, err := s.db.Query(dlSelect+q+" ORDER BY "+order+" "+dir+listLimitClause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlice(rows)
}

// ListActive returns every download in `downloading` status across all users.
// The worker uses this to schedule downloads each tick.
func (s *Store) ListActive() ([]Download, error) {
	rows, err := s.db.Query(dlSelect+"WHERE status=? ORDER BY id", StatusDownloading)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlice(rows)
}

// WantedRowsByHash returns every row of one (user, info_hash) that still WANTS
// the torrent's data — status `downloading` or `queued`. The aggregate-by-torrent
// completion check uses this to avoid finalizing a torrent while a sibling file
// is still queued (not yet promoted/downloaded): the tick groups only the active
// (downloading) rows, so a queued sibling is invisible to GroupRows and would
// otherwise let the move fire with a file missing. info_hash must be non-empty
// (a hashless pre-metadata row has no siblings to speak of).
func (s *Store) WantedRowsByHash(userID int, infoHash string) ([]Download, error) {
	if s == nil || infoHash == "" {
		return nil, nil
	}
	rows, err := s.db.Query(dlSelect+"WHERE user_id=? AND info_hash=? AND status IN (?, ?)",
		userID, infoHash, StatusDownloading, StatusQueued)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlice(rows)
}

// ListAll returns every download — used by the streamer to compute the
// "protected from eviction" set on startup (any non-final entry should keep
// its torrent data on disk).
func (s *Store) ListAll() ([]Download, error) {
	rows, err := s.db.Query(dlSelect + "ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlice(rows)
}

// ListFilteredAll returns downloads across ALL users, filtered by optional
// criteria. Used by admin listing. Empty filters are ignored.
func (s *Store) ListFilteredAll(f ListFilter) ([]Download, error) {
	q := "WHERE 1=1"
	args := []any{}
	if f.Status != "" {
		q += " AND status=?"
		args = append(args, f.Status)
	}
	if f.Tracker != "" {
		q += " AND tracker=?"
		args = append(args, f.Tracker)
	}
	if f.Category != "" {
		q += " AND category=?"
		args = append(args, f.Category)
	}
	if f.UserIDFilter != "" {
		q += " AND user_id=?"
		args = append(args, f.UserIDFilter)
	}
	if f.Search != "" {
		q += " AND (name LIKE ? OR file_path LIKE ?)"
		s := "%" + f.Search + "%"
		args = append(args, s, s)
	}
	order := "created_at"
	switch f.SortCol {
	case "name":
		order = "name"
	case "size":
		order = "file_size"
	case "progress":
		order = "bytes_downloaded"
	case "status":
		order = "status"
	case "tracker":
		order = "tracker"
	case "category":
		order = "category"
	case "user_id", "username":
		order = "user_id"
	}
	dir := "DESC"
	if f.SortDir == "asc" {
		dir = "ASC"
	}
	rows, err := s.db.Query(dlSelect+q+" ORDER BY "+order+" "+dir+listLimitClause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlice(rows)
}

// DistinctUsers returns all distinct user_ids that have downloads.
func (s *Store) DistinctUsers() ([]int, error) {
	rows, err := s.db.Query("SELECT DISTINCT user_id FROM downloads ORDER BY user_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var uid int
		if rows.Scan(&uid) == nil {
			out = append(out, uid)
		}
	}
	return out, rows.Err()
}

// DistinctTrackers returns all distinct tracker values for the user.
func (s *Store) DistinctTrackers(userID int) ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT tracker FROM downloads WHERE user_id=? AND tracker!='' ORDER BY tracker", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

// DistinctCategories returns all distinct category values for the user.
func (s *Store) DistinctCategories(userID int) ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT category FROM downloads WHERE user_id=? AND category!='' ORDER BY category", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

func scanSlice(rows *sql.Rows) ([]Download, error) {
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
