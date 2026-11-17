package downloads

import "database/sql"

// GetCompletedPathRel resolves the on-disk path of ONE file from a completed
// download. Per-file rows behave exactly like GetCompletedPath. When the
// torrent was downloaded as a single whole-torrent item, only the
// FileIndexWholeTorrent row exists and its file_path is the torrent's
// destination DIRECTORY (moveCompletedTree preserved the in-torrent structure
// under it), so the file is located by joining that directory with relPath —
// the torrent-relative path the caller reads from the cached metainfo; the
// store alone can't map a file index to a path without activating the torrent.
//
// relPath is untrusted (it ultimately comes from torrent metadata): traversal
// is rejected and the resolved path must be an existing regular file under the
// destination directory. Empty relPath skips the whole-torrent fallback.
func (s *Store) GetCompletedPathRel(infoHash string, fileIndex int, relPath string, userID int) (string, error) {
	return s.GetCompletedPathRelFunc(infoHash, fileIndex, func() string { return relPath }, userID)
}

// GetCompletedPathRelFunc is GetCompletedPathRel with the relative path
// resolved lazily: relPath runs only once a completed whole-torrent row is
// known to exist. Resolving it costs a metadata-cache SELECT plus decoding the
// torrent's whole file list, which the stream endpoint used to pay on every
// Range request even though most requests never need it (per-file row, or no
// completed download at all).
func (s *Store) GetCompletedPathRelFunc(infoHash string, fileIndex int, relPath func() string, userID int) (string, error) {
	path, err := s.GetCompletedPath(infoHash, fileIndex, userID)
	if err != nil || path != "" {
		return path, err
	}
	if s == nil || fileIndex < 0 {
		return "", nil
	}
	var destDir, name string
	if userID >= 0 {
		err = s.db.QueryRow(sqlCompletedWholeForUser, infoHash, FileIndexWholeTorrent, userID).Scan(&destDir, &name)
	} else {
		err = s.db.QueryRow(sqlCompletedWholeAny, infoHash, FileIndexWholeTorrent).Scan(&destDir, &name)
	}
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	rel := relPath()
	if rel == "" {
		return "", nil
	}
	return resolveWholeTorrentFile(destDir, name, rel), nil
}
