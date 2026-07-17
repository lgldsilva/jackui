// Package watchlist persists per-user search queries that the server polls in
// the background. New results above the user's seeders threshold trigger a
// push notification to the user's ntfy.sh topic.
package watchlist

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/lgldsilva/jackui/internal/dbutil"
)

// Watchlist is one saved search the worker polls on its own schedule.
type Watchlist struct {
	ID          int       `json:"id"`
	UserID      int       `json:"userId"`
	Query       string    `json:"query"`
	Category    string    `json:"category"` // optional Jackett category filter
	MinSeeders  int       `json:"minSeeders"`
	NtfyTopic   string    `json:"ntfyTopic"` // optional override of the global default
	Schedule              // per-item check schedule (sched_* columns)
	NextCheckAt time.Time `json:"nextCheckAt"` // when the worker should check this item next
	LastChecked time.Time `json:"lastChecked"`
	CreatedAt   time.Time `json:"createdAt"`
	HitCount    int       `json:"hitCount,omitempty"` // computed from watchlist_seen
	// Auto-download: when enabled, new hits that pass the quality filters are
	// enqueued straight into the downloads queue instead of just notifying.
	AutoDownload  bool   `json:"autoDownload"`
	MinResolution string `json:"minResolution"` // "", "480p", "720p", "1080p", "2160p"
	MaxSizeBytes  int64  `json:"maxSizeBytes"`  // 0 = unlimited
	Codec         string `json:"codec"`         // "", "x264", "x265", "av1"
}

// Params carries the user-editable fields of a watchlist for Create/Update.
type Params struct {
	Query         string
	Category      string
	MinSeeders    int
	NtfyTopic     string
	Schedule      // per-item check schedule
	AutoDownload  bool
	MinResolution string
	MaxSizeBytes  int64
	Codec         string
}

// Hit is a single new torrent detected by the worker for a given watchlist.
type Hit struct {
	InfoHash       string    `json:"infoHash"`
	Title          string    `json:"title"`
	Magnet         string    `json:"magnet"`
	Seeders        int       `json:"seeders"`
	Size           int64     `json:"size"`
	SeenAt         time.Time `json:"seenAt"`
	AutoDownloaded bool      `json:"autoDownloaded"`
}

type Store struct {
	db *dbutil.DB
	// DefaultEvery is the server-wide interval applied to items whose schedule
	// is "interval" with Minutes <= 0 ("server default"). Set once at boot from
	// the config (notifications.watchlist_minutes); zero falls back to 15 min.
	DefaultEvery time.Duration
}

// New wires the watchlist store onto the shared Postgres pool. Schema is applied
// centrally (internal/db migrations).
func New(pool *sql.DB) (*Store, error) {
	return &Store{db: dbutil.Wrap(pool)}, nil
}

// nextFor computes the next due time for a schedule using the store-wide
// default interval as the fallback for "server default" interval items.
func (s *Store) nextFor(sched Schedule, now time.Time) time.Time {
	return nextCheckTime(sched, now, s.DefaultEvery)
}

// normalize clamps the schedule and lower-cases/validates the auto-download
// filter fields. Invalid values come back as errors so a typo never silently
// disables a filter.
func (p *Params) normalize() error {
	if p.Query == "" {
		return errors.New("query é obrigatória")
	}
	if p.MinSeeders < 0 {
		p.MinSeeders = 0
	}
	if p.MaxSizeBytes < 0 {
		p.MaxSizeBytes = 0
	}
	p.Schedule = p.Normalized() // Normalized() promoted from the embedded Schedule
	p.MinResolution = strings.ToLower(p.MinResolution)
	if _, ok := resolutionRank[p.MinResolution]; p.MinResolution != "" && !ok {
		return errors.New("invalid minResolution")
	}
	p.Codec = strings.ToLower(p.Codec)
	switch p.Codec {
	case "", "x264", "x265", "av1":
	default:
		return errors.New("invalid codec")
	}
	return nil
}

// Create inserts a new watchlist row. The schedule is normalized and
// next_check_at is computed from it so the worker knows when the item is due.
func (s *Store) Create(userID int, p Params) (*Watchlist, error) {
	if err := p.normalize(); err != nil {
		return nil, err
	}
	next := s.nextFor(p.Schedule, time.Now())
	var id int64
	err := s.db.QueryRow(`
		INSERT INTO watchlists(user_id, query, category, min_seeders, ntfy_topic,
		                       sched_kind, sched_minutes, sched_weekday, sched_hour, sched_minute, next_check_at,
		                       auto_download, min_resolution, max_size_bytes, codec)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id
	`, userID, p.Query, p.Category, p.MinSeeders, p.NtfyTopic,
		p.Kind, p.Minutes, p.Weekday, p.Hour, p.Minute, next.UTC(),
		boolToInt(p.AutoDownload), p.MinResolution, p.MaxSizeBytes, p.Codec).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.Get(userID, int(id))
}

// Update modifies an existing watchlist owned by userID. A schedule change
// recomputes next_check_at immediately. Changing the query resets last_checked
// so the next worker pass re-baselines (marks the current results as seen)
// instead of auto-downloading the whole new result set.
func (s *Store) Update(userID, id int, p Params) error {
	if err := p.normalize(); err != nil {
		return err
	}
	next := s.nextFor(p.Schedule, time.Now())
	res, err := s.db.Exec(`
		UPDATE watchlists SET query=?, category=?, min_seeders=?, ntfy_topic=?,
		       sched_kind=?, sched_minutes=?, sched_weekday=?, sched_hour=?, sched_minute=?, next_check_at=?,
		       auto_download=?, min_resolution=?, max_size_bytes=?, codec=?,
		       last_checked=CASE WHEN query=? THEN last_checked ELSE NULL END
		WHERE id=? AND user_id=?
	`, p.Query, p.Category, p.MinSeeders, p.NtfyTopic,
		p.Kind, p.Minutes, p.Weekday, p.Hour, p.Minute, next.UTC(),
		boolToInt(p.AutoDownload), p.MinResolution, p.MaxSizeBytes, p.Codec,
		p.Query, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("watchlist não encontrada")
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Delete removes a watchlist (and cascades the seen rows via FK).
func (s *Store) Delete(userID, id int) error {
	res, err := s.db.Exec("DELETE FROM watchlists WHERE id=? AND user_id=?", id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("watchlist não encontrada")
	}
	return nil
}

// watchlistCols is the canonical column list every scanWatchlist call expects.
const watchlistCols = `id, user_id, query, category, min_seeders, ntfy_topic,
       sched_kind, sched_minutes, sched_weekday, sched_hour, sched_minute,
       next_check_at, last_checked, created_at,
       auto_download, min_resolution, max_size_bytes, codec`

// scanWatchlist fills w from a row produced with watchlistCols. scan is
// row.Scan / rows.Scan (or a wrapper appending extra dests, see List).
func scanWatchlist(scan func(dest ...any) error, w *Watchlist) error {
	var nextCheck, lastChecked sql.NullTime
	var autoDL int
	if err := scan(&w.ID, &w.UserID, &w.Query, &w.Category, &w.MinSeeders, &w.NtfyTopic,
		&w.Kind, &w.Minutes, &w.Weekday, &w.Hour, &w.Minute,
		&nextCheck, &lastChecked, &w.CreatedAt,
		&autoDL, &w.MinResolution, &w.MaxSizeBytes, &w.Codec); err != nil {
		return err
	}
	w.NextCheckAt = nextCheck.Time
	w.LastChecked = lastChecked.Time
	w.AutoDownload = autoDL != 0
	return nil
}

// Get returns a single watchlist owned by userID.
func (s *Store) Get(userID, id int) (*Watchlist, error) {
	row := s.db.QueryRow(`SELECT `+watchlistCols+` FROM watchlists WHERE id=? AND user_id=?`, id, userID)
	w := &Watchlist{}
	if err := scanWatchlist(row.Scan, w); err != nil {
		return nil, err
	}
	return w, nil
}

// getByID returns a watchlist regardless of owner — worker internals only.
func (s *Store) getByID(id int) (*Watchlist, error) {
	row := s.db.QueryRow(`SELECT `+watchlistCols+` FROM watchlists WHERE id=?`, id)
	w := &Watchlist{}
	if err := scanWatchlist(row.Scan, w); err != nil {
		return nil, err
	}
	return w, nil
}

// List returns all watchlists for a user, newest first, with hit counts.
func (s *Store) List(userID int) ([]Watchlist, error) {
	rows, err := s.db.Query(`
		SELECT `+watchlistCols+`,
		       (SELECT COUNT(*) FROM watchlist_seen WHERE watchlist_id=watchlists.id) AS hits
		FROM watchlists
		WHERE user_id=?
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Watchlist{}
	for rows.Next() {
		w := Watchlist{}
		scan := func(dest ...any) error { return rows.Scan(append(dest, &w.HitCount)...) }
		if err := scanWatchlist(scan, &w); err != nil {
			continue
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ListAll returns every watchlist across all users — used by manual re-checks.
func (s *Store) ListAll() ([]Watchlist, error) {
	return s.queryAll(`SELECT ` + watchlistCols + ` FROM watchlists ORDER BY id`)
}

// ListDue returns the watchlists whose next check is due at `now`. Rows with a
// NULL next_check_at (pre-migration) are due immediately.
func (s *Store) ListDue(now time.Time) ([]Watchlist, error) {
	return s.queryAll(`
		SELECT `+watchlistCols+` FROM watchlists
		WHERE next_check_at IS NULL OR next_check_at <= ?
		ORDER BY id
	`, now.UTC())
}

func (s *Store) queryAll(query string, args ...any) ([]Watchlist, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Watchlist{}
	for rows.Next() {
		w := Watchlist{}
		if err := scanWatchlist(rows.Scan, &w); err != nil {
			continue
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// MarkSeen records an info_hash as already seen. Returns true if this was the
// first time (i.e., a new hit), false if it was already known.
func (s *Store) MarkSeen(watchlistID int, infoHash, title, magnet string, seeders int, size int64) (isNew bool, err error) {
	res, err := s.db.Exec(`
		INSERT INTO watchlist_seen(watchlist_id, info_hash, title, magnet, seeders, size)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(watchlist_id, info_hash) DO NOTHING
	`, watchlistID, infoHash, title, magnet, seeders, size)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// MarkAutoDownloaded flags a seen hit as auto-enqueued, for display in the UI.
func (s *Store) MarkAutoDownloaded(watchlistID int, infoHash string) error {
	_, err := s.db.Exec(`
		UPDATE watchlist_seen SET auto_downloaded=1 WHERE watchlist_id=? AND info_hash=?
	`, watchlistID, infoHash)
	return err
}

// MarkChecked refreshes last_checked to now and re-arms next_check_at — call
// after a worker pass over the item.
func (s *Store) MarkChecked(watchlistID int, next time.Time) error {
	_, err := s.db.Exec(`UPDATE watchlists SET last_checked=CURRENT_TIMESTAMP, next_check_at=? WHERE id=?`,
		next.UTC(), watchlistID)
	return err
}

// Hits returns the most recent hits for a watchlist owned by userID.
func (s *Store) Hits(userID, watchlistID, limit int) ([]Hit, error) {
	// Ownership check first
	if _, err := s.Get(userID, watchlistID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT info_hash, title, magnet, seeders, size, seen_at, auto_downloaded
		FROM watchlist_seen
		WHERE watchlist_id=?
		ORDER BY seen_at DESC LIMIT ?
	`, watchlistID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Hit{}
	for rows.Next() {
		h := Hit{}
		var autoDL int
		if err := rows.Scan(&h.InfoHash, &h.Title, &h.Magnet, &h.Seeders, &h.Size, &h.SeenAt, &autoDL); err != nil {
			continue
		}
		h.AutoDownloaded = autoDL != 0
		out = append(out, h)
	}
	return out, rows.Err()
}
