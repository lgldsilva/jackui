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
	_ "modernc.org/sqlite"
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
	db *sql.DB
	// DefaultEvery is the server-wide interval applied to items whose schedule
	// is "interval" with Minutes <= 0 ("server default"). Set once at boot from
	// the config (notifications.watchlist_minutes); zero falls back to 15 min.
	DefaultEvery time.Duration
}

func New(path string) (*Store, error) {
	db, err := sql.Open(dbutil.DriverName, path+dbutil.PragmaWAL+dbutil.PragmaFK)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() { s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS watchlists (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id        INTEGER NOT NULL,
			query          TEXT    NOT NULL,
			category       TEXT    NOT NULL DEFAULT '',
			min_seeders    INTEGER NOT NULL DEFAULT 1,
			ntfy_topic     TEXT    NOT NULL DEFAULT '',
			sched_kind     TEXT    NOT NULL DEFAULT 'interval',
			sched_minutes  INTEGER NOT NULL DEFAULT 0,
			sched_weekday  INTEGER NOT NULL DEFAULT 0,
			sched_hour     INTEGER NOT NULL DEFAULT 0,
			sched_minute   INTEGER NOT NULL DEFAULT 0,
			next_check_at  DATETIME,
			last_checked   DATETIME,
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			auto_download  INTEGER NOT NULL DEFAULT 0,
			min_resolution TEXT    NOT NULL DEFAULT '',
			max_size_bytes INTEGER NOT NULL DEFAULT 0,
			codec          TEXT    NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_wl_user ON watchlists(user_id);

		CREATE TABLE IF NOT EXISTS watchlist_seen (
			watchlist_id    INTEGER NOT NULL REFERENCES watchlists(id) ON DELETE CASCADE,
			info_hash       TEXT    NOT NULL,
			title           TEXT    NOT NULL DEFAULT '',
			magnet          TEXT    NOT NULL DEFAULT '',
			seeders         INTEGER NOT NULL DEFAULT 0,
			size            INTEGER NOT NULL DEFAULT 0,
			seen_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			auto_downloaded INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (watchlist_id, info_hash)
		);
		CREATE INDEX IF NOT EXISTS idx_wls_recent ON watchlist_seen(watchlist_id, seen_at DESC);
	`)
	if err != nil {
		return err
	}
	// Idempotent ALTERs for DBs that pre-date per-item scheduling and
	// auto-download. sched_minutes defaults to 0 = "server default interval",
	// so existing rows keep honouring the configured watchlist_minutes.
	// next_check_at stays NULL → due on the first scheduler pass after boot.
	for _, m := range []struct{ table, col, ddl string }{
		{"watchlists", "sched_kind", `ALTER TABLE watchlists ADD COLUMN sched_kind TEXT NOT NULL DEFAULT 'interval'`},
		{"watchlists", "sched_minutes", `ALTER TABLE watchlists ADD COLUMN sched_minutes INTEGER NOT NULL DEFAULT 0`},
		{"watchlists", "sched_weekday", `ALTER TABLE watchlists ADD COLUMN sched_weekday INTEGER NOT NULL DEFAULT 0`},
		{"watchlists", "sched_hour", `ALTER TABLE watchlists ADD COLUMN sched_hour INTEGER NOT NULL DEFAULT 0`},
		{"watchlists", "sched_minute", `ALTER TABLE watchlists ADD COLUMN sched_minute INTEGER NOT NULL DEFAULT 0`},
		{"watchlists", "next_check_at", `ALTER TABLE watchlists ADD COLUMN next_check_at DATETIME`},
		{"watchlists", "auto_download", `ALTER TABLE watchlists ADD COLUMN auto_download INTEGER NOT NULL DEFAULT 0`},
		{"watchlists", "min_resolution", `ALTER TABLE watchlists ADD COLUMN min_resolution TEXT NOT NULL DEFAULT ''`},
		{"watchlists", "max_size_bytes", `ALTER TABLE watchlists ADD COLUMN max_size_bytes INTEGER NOT NULL DEFAULT 0`},
		{"watchlists", "codec", `ALTER TABLE watchlists ADD COLUMN codec TEXT NOT NULL DEFAULT ''`},
		{"watchlist_seen", "auto_downloaded", `ALTER TABLE watchlist_seen ADD COLUMN auto_downloaded INTEGER NOT NULL DEFAULT 0`},
	} {
		if s.hasColumn(m.table, m.col) {
			continue
		}
		if _, err := s.db.Exec(m.ddl); err != nil {
			return err
		}
	}
	return nil
}

// hasColumn checks whether a column exists in the given table — used for
// idempotent migrations.
func (s *Store) hasColumn(table, col string) bool {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err == nil && n == col {
			return true
		}
	}
	return false
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
	res, err := s.db.Exec(`
		INSERT INTO watchlists(user_id, query, category, min_seeders, ntfy_topic,
		                       sched_kind, sched_minutes, sched_weekday, sched_hour, sched_minute, next_check_at,
		                       auto_download, min_resolution, max_size_bytes, codec)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, userID, p.Query, p.Category, p.MinSeeders, p.NtfyTopic,
		p.Kind, p.Minutes, p.Weekday, p.Hour, p.Minute, next.UTC().Format(dbutil.TimeFormat),
		boolToInt(p.AutoDownload), p.MinResolution, p.MaxSizeBytes, p.Codec)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
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
		p.Kind, p.Minutes, p.Weekday, p.Hour, p.Minute, next.UTC().Format(dbutil.TimeFormat),
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
       COALESCE(next_check_at, ''), COALESCE(last_checked, ''), created_at,
       auto_download, min_resolution, max_size_bytes, codec`

// scanWatchlist fills w from a row produced with watchlistCols. scan is
// row.Scan / rows.Scan (or a wrapper appending extra dests, see List).
func scanWatchlist(scan func(dest ...any) error, w *Watchlist) error {
	var nextCheck, lastChecked, createdAt string
	var autoDL int
	if err := scan(&w.ID, &w.UserID, &w.Query, &w.Category, &w.MinSeeders, &w.NtfyTopic,
		&w.Kind, &w.Minutes, &w.Weekday, &w.Hour, &w.Minute,
		&nextCheck, &lastChecked, &createdAt,
		&autoDL, &w.MinResolution, &w.MaxSizeBytes, &w.Codec); err != nil {
		return err
	}
	w.NextCheckAt = dbutil.ParseTime(nextCheck)
	w.LastChecked = dbutil.ParseTime(lastChecked)
	w.CreatedAt = dbutil.ParseTime(createdAt)
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
	`, now.UTC().Format(dbutil.TimeFormat))
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
		INSERT OR IGNORE INTO watchlist_seen(watchlist_id, info_hash, title, magnet, seeders, size)
		VALUES(?, ?, ?, ?, ?, ?)
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
		next.UTC().Format(dbutil.TimeFormat), watchlistID)
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
		var seenAt string
		var autoDL int
		if err := rows.Scan(&h.InfoHash, &h.Title, &h.Magnet, &h.Seeders, &h.Size, &seenAt, &autoDL); err != nil {
			continue
		}
		h.SeenAt = dbutil.ParseTime(seenAt)
		h.AutoDownloaded = autoDL != 0
		out = append(out, h)
	}
	return out, rows.Err()
}
