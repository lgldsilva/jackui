package downloads

import (
	"time"

	"github.com/lgldsilva/jackui/internal/dbutil"
)

// Source lifecycle within a download's catalog of magnets.
const (
	SourceActive    = "active"    // currently being downloaded
	SourceCandidate = "candidate" // known, eligible to try
	SourceCooldown  = "cooldown"  // tried recently with no seed — wait before retrying
	SourceFailed    = "failed"    // gave up after too many tries
)

// Source is one magnet known for a download — the original plus any alternatives
// discovered via Jackett re-search. Used for round-robin rotation (Phase 2).
type Source struct {
	ID         int        `json:"id"`
	DownloadID int        `json:"downloadId"`
	Magnet     string     `json:"magnet"`
	InfoHash   string     `json:"infoHash"`
	Title      string     `json:"title"`
	Tracker    string     `json:"tracker"`
	Seeders    int        `json:"seeders"`
	Size       int64      `json:"size"`
	Status     string     `json:"status"`
	Tries      int        `json:"tries"`
	LastTried  *time.Time `json:"lastTried,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

const srcSelect = `SELECT id, download_id, magnet, info_hash, title, tracker, seeders, size,
	status, tries, COALESCE(last_tried, ''), created_at FROM download_sources `

func scanSource(rows interface{ Scan(...any) error }) (*Source, error) {
	s := &Source{}
	var lastTried, createdAt string
	if err := rows.Scan(&s.ID, &s.DownloadID, &s.Magnet, &s.InfoHash, &s.Title, &s.Tracker,
		&s.Seeders, &s.Size, &s.Status, &s.Tries, &lastTried, &createdAt); err != nil {
		return nil, err
	}
	if t := dbutil.ParseTime(lastTried); !t.IsZero() {
		s.LastTried = &t
	}
	s.CreatedAt = dbutil.ParseTime(createdAt)
	return s, nil
}

// EnsureSource records a magnet as a known source for the download. Idempotent on
// (download_id, info_hash): an existing row keeps its status/tries but refreshes
// seeders (the swarm health changes over time). status is the initial status for
// a NEW row (e.g. SourceActive for the original, SourceCandidate for discovered).
func (s *Store) EnsureSource(src Source, status string) error {
	_, err := s.db.Exec(`
		INSERT INTO download_sources(download_id, magnet, info_hash, title, tracker, seeders, size, status)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(download_id, info_hash) DO UPDATE SET seeders=excluded.seeders`,
		src.DownloadID, src.Magnet, src.InfoHash, src.Title, src.Tracker, src.Seeders, src.Size, status)
	return err
}

// ListSources returns all known sources for a download, active first then by seeders.
func (s *Store) ListSources(downloadID int) ([]Source, error) {
	rows, err := s.db.Query(srcSelect+`WHERE download_id=?
		ORDER BY CASE status WHEN 'active' THEN 0 ELSE 1 END, seeders DESC, id ASC`, downloadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *src)
	}
	return out, rows.Err()
}

// HasSources reports whether any source row exists for the download (used to
// decide whether to seed the original source lazily).
func (s *Store) HasSources(downloadID int) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM download_sources WHERE download_id=?`, downloadID).Scan(&n)
	return n > 0, err
}

// NextSource returns the best source to try next: not failed, not the currently
// active one, with cooldown elapsed (or never tried). Highest seeders first.
// cooldownMin gates how long a tried-and-stalled source waits before reuse.
// Returns nil when no source is ready.
func (s *Store) NextSource(downloadID, cooldownMin int) (*Source, error) {
	rows, err := s.db.Query(srcSelect+`WHERE download_id=? AND status!='failed' AND status!='active'
		ORDER BY seeders DESC, COALESCE(last_tried, '') ASC, id ASC`, downloadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cutoff := time.Now().Add(-time.Duration(cooldownMin) * time.Minute)
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		// A cooldown source is only ready once cooldownMin has elapsed since last_tried.
		if src.Status == SourceCooldown && src.LastTried != nil && src.LastTried.After(cutoff) {
			continue
		}
		return src, nil
	}
	return nil, rows.Err()
}

// ActivateSource marks sourceID as the active source (others → candidate) and
// points the download's active_magnet at it. Worker re-inits with this magnet.
func (s *Store) ActivateSource(downloadID, sourceID int, magnet string) error {
	if _, err := s.db.Exec(`UPDATE download_sources SET status=? WHERE download_id=? AND status=?`,
		SourceCandidate, downloadID, SourceActive); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE download_sources SET status=? WHERE id=?`, SourceActive, sourceID); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE downloads SET active_magnet=? WHERE id=?`, magnet, downloadID)
	return err
}

// MarkSourceTried bumps the try counter and parks the source in cooldown (or
// failed once it exceeds maxTries). Called when a source stalls.
func (s *Store) MarkSourceTried(sourceID, maxTries int) error {
	status := SourceCooldown
	var tries int
	_ = s.db.QueryRow(`SELECT tries FROM download_sources WHERE id=?`, sourceID).Scan(&tries)
	if maxTries > 0 && tries+1 >= maxTries {
		status = SourceFailed
	}
	_, err := s.db.Exec(`UPDATE download_sources SET tries=tries+1, last_tried=CURRENT_TIMESTAMP, status=? WHERE id=?`,
		status, sourceID)
	return err
}

// SourceByInfoHash returns the source row for (download, info_hash), or nil.
func (s *Store) SourceByInfoHash(downloadID int, infoHash string) (*Source, error) {
	row := s.db.QueryRow(srcSelect+`WHERE download_id=? AND info_hash=?`, downloadID, infoHash)
	src, err := scanSource(row)
	if err != nil {
		return nil, nil //nolint:nilerr // no row → no source, not an error for callers
	}
	return src, nil
}
