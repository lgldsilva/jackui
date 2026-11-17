package downloads

// Static queries (no concatenation), one per ownership scope — same convention
// as the single-file lookups in store.go. ORDER BY id makes the pick among
// several owners' rows deterministic.
const (
	sqlCompletedRowsForUser = `
		SELECT file_index, file_path, name FROM downloads
		WHERE info_hash=? AND status='completed' AND file_path != ''
		  AND user_id=?
		ORDER BY id`
	sqlCompletedRowsAny = `
		SELECT file_index, file_path, name FROM downloads
		WHERE info_hash=? AND status='completed' AND file_path != ''
		ORDER BY id`
)

// completedRows is every completed row of one torrent: the per-file paths by
// file index, plus the whole-torrent row's destination directory when present.
type completedRows struct {
	perFile   map[int]string
	wholeDir  string
	wholeName string
	hasWhole  bool
}

// GetCompletedPathsRel resolves EVERY file of a torrent in one query. The
// result holds, for each index i of relPaths, what
// GetCompletedPathRel(infoHash, i, relPaths[i], userID) returns, omitting the
// files that do not resolve. It exists for callers that need the whole layout
// (the streamer's relocated seed storage): asking per file costs up to two
// queries each, which adds up on a pack with hundreds of files.
//
// userID follows GetCompletedPath: >= 0 scopes to that owner, < 0 is only for
// trusted internal callers.
func (s *Store) GetCompletedPathsRel(infoHash string, relPaths []string, userID int) (map[int]string, error) {
	if s == nil || len(relPaths) == 0 {
		return map[int]string{}, nil
	}
	rows, err := s.completedRows(infoHash, userID)
	if err != nil {
		return nil, err
	}
	out := make(map[int]string, len(relPaths))
	for i, rel := range relPaths {
		if p := rows.resolve(i, rel); p != "" {
			out[i] = p
		}
	}
	return out, nil
}

func (s *Store) completedRows(infoHash string, userID int) (completedRows, error) {
	res := completedRows{perFile: map[int]string{}}
	query, args := sqlCompletedRowsAny, []any{infoHash}
	if userID >= 0 {
		query, args = sqlCompletedRowsForUser, []any{infoHash, userID}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return res, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var idx int
		var path, name string
		if err := rows.Scan(&idx, &path, &name); err != nil {
			return res, err
		}
		res.add(idx, path, name)
	}
	return res, rows.Err()
}

// add keeps the first row seen per file index (and the first whole-torrent row).
func (r *completedRows) add(idx int, path, name string) {
	if idx == FileIndexWholeTorrent {
		if !r.hasWhole {
			r.wholeDir, r.wholeName, r.hasWhole = path, name, true
		}
		return
	}
	if _, seen := r.perFile[idx]; !seen {
		r.perFile[idx] = path
	}
}

// resolve mirrors GetCompletedPathRel: the per-file row wins, then the file is
// looked up inside the whole-torrent row's moved tree.
func (r *completedRows) resolve(idx int, relPath string) string {
	if p := r.perFile[idx]; p != "" {
		return p
	}
	if !r.hasWhole || relPath == "" {
		return ""
	}
	return resolveWholeTorrentFile(r.wholeDir, r.wholeName, relPath)
}
