package history

import (
	"database/sql"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/luizg/jackui/internal/dbutil"
	"github.com/luizg/jackui/internal/jackett"
)

type Store struct {
	db *sql.DB
}

type CachedResult struct {
	jackett.Result
	ID      int64     `json:"id,omitempty"`
	SavedAt time.Time `json:"savedAt"`
	Cached  bool      `json:"cached"`
	Query   string    `json:"query,omitempty"` // populated by SearchAll to show origin
}

func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	return s, s.migrate()
}

func (s *Store) Close() {
	_ = s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS results (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			query        TEXT NOT NULL,
			title        TEXT NOT NULL DEFAULT '',
			tracker      TEXT NOT NULL DEFAULT '',
			category     TEXT NOT NULL DEFAULT '',
			size         INTEGER NOT NULL DEFAULT 0,
			seeders      INTEGER NOT NULL DEFAULT 0,
			leechers     INTEGER NOT NULL DEFAULT 0,
			age          TEXT NOT NULL DEFAULT '',
			magnet_uri   TEXT NOT NULL DEFAULT '',
			link         TEXT NOT NULL DEFAULT '',
			info_hash    TEXT NOT NULL DEFAULT '',
			publish_date TEXT NOT NULL DEFAULT '',
			saved_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			user_id      INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_results_query    ON results(LOWER(query));
		CREATE INDEX IF NOT EXISTS idx_results_info_hash ON results(info_hash);
		CREATE INDEX IF NOT EXISTS idx_results_saved_at  ON results(saved_at);
		-- idx_results_user_id is created AFTER the ALTER below — older DBs may not have user_id yet

		-- FTS5 virtual table mirroring the searchable text columns of results.
		-- content='results' means we don't duplicate data; FTS reads from results by rowid.
		CREATE VIRTUAL TABLE IF NOT EXISTS results_fts USING fts5(
			title, query, tracker,
			content='results',
			content_rowid='id',
			tokenize='unicode61 remove_diacritics 2'
		);

		-- Sync triggers — keep FTS index aligned with results table mutations.
		CREATE TRIGGER IF NOT EXISTS results_ai AFTER INSERT ON results BEGIN
			INSERT INTO results_fts(rowid, title, query, tracker)
			VALUES (new.id, new.title, new.query, new.tracker);
		END;
		CREATE TRIGGER IF NOT EXISTS results_ad AFTER DELETE ON results BEGIN
			INSERT INTO results_fts(results_fts, rowid, title, query, tracker)
			VALUES ('delete', old.id, old.title, old.query, old.tracker);
		END;
		CREATE TRIGGER IF NOT EXISTS results_au AFTER UPDATE ON results BEGIN
			INSERT INTO results_fts(results_fts, rowid, title, query, tracker)
			VALUES ('delete', old.id, old.title, old.query, old.tracker);
			INSERT INTO results_fts(rowid, title, query, tracker)
			VALUES (new.id, new.title, new.query, new.tracker);
		END;
	`)
	if err != nil {
		return err
	}

	// Idempotent ALTER for older DBs that pre-date the user_id column.
	// Must happen BEFORE creating the user_id index — running against an old DB without the column would fail.
	if !s.hasColumn("results", "user_id") {
		if _, err := s.db.Exec(`ALTER TABLE results ADD COLUMN user_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	// Now safe — column exists either from CREATE (fresh DB) or ALTER (migrated DB)
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_results_user_id ON results(user_id)`); err != nil {
		return err
	}

	// Incognito flag — entries recorded during an incognito session.
	// Filtered out from all read queries; deleted when the user ends their incognito session.
	if !s.hasColumn("results", "incognito") {
		if _, err := s.db.Exec(`ALTER TABLE results ADD COLUMN incognito INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}

	// Backfill FTS if it's empty but results has rows (e.g., upgrading from a pre-FTS DB)
	var ftsCount, resultsCount int
	_ = s.db.QueryRow("SELECT count(*) FROM results_fts").Scan(&ftsCount)
	_ = s.db.QueryRow("SELECT count(*) FROM results").Scan(&resultsCount)
	if ftsCount == 0 && resultsCount > 0 {
		if _, err := s.db.Exec("INSERT INTO results_fts(results_fts) VALUES('rebuild')"); err != nil {
			return err
		}
	}
	return nil
}

// hasColumn checks whether a column exists in the given table — used for idempotent migrations.
func (s *Store) hasColumn(table, col string) bool {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err == nil && n == col {
			return true
		}
	}
	return false
}

// buildFTSQuery sanitizes a user-supplied query for FTS5 MATCH.
// Each whitespace-separated token becomes a quoted prefix term joined by implicit AND.
// Example: `breaking bad` -> `"breaking"* "bad"*`
func buildFTSQuery(input string) string {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return ""
	}
	tokens := make([]string, 0, len(fields))
	for _, f := range fields {
		// Strip FTS5 special chars that aren't valid inside a quoted term
		f = strings.ReplaceAll(f, `"`, "")
		if f == "" {
			continue
		}
		tokens = append(tokens, `"`+f+`"*`)
	}
	return strings.Join(tokens, " ")
}

// Save persists Jackett results for a query, attributed to a user.
// Dedup is per-(user, info_hash) so two users searching the same content each get their own row.
// userID=0 means "no auth" (legacy/anonymous mode).
// When incognito=true the row is written with incognito=1 so it is visible during
// the current session but excluded from normal queries and deleted on session end.
func (s *Store) Save(query string, results []jackett.Result, userID int, incognito bool) error {
	if len(results) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	seen := make(map[string]bool)
	incognitoVal := 0
	if incognito {
		incognitoVal = 1
	}

	for _, r := range results {
		if r.InfoHash != "" {
			if seen[r.InfoHash] {
				continue
			}
			seen[r.InfoHash] = true

			var count int
			tx.QueryRow("SELECT COUNT(*) FROM results WHERE info_hash = ? AND user_id = ? AND incognito = ?", r.InfoHash, userID, incognitoVal).Scan(&count)
			if count > 0 {
				continue
			}
		}

		_, err := tx.Exec(`
			INSERT INTO results (query, title, tracker, category, size, seeders, leechers, age, magnet_uri, link, info_hash, publish_date, user_id, incognito)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, strings.ToLower(query), r.Title, r.Tracker, r.Category, r.Size, r.Seeders, r.Leechers, r.Age, r.MagnetURI, r.Link, r.InfoHash, r.PublishDate, userID, incognitoVal)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// DeleteIncognito removes all incognito-flagged entries for a user.
// Called when the user ends their incognito session (logout or toggle-off).
func (s *Store) DeleteIncognito(userID int) error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM results WHERE user_id = ? AND incognito = 1`, userID)
	return err
}

// Search returns cached results for a query, ordered by seeders descending.
// Filters by userID unless includeAll=true (admin override).
func (s *Store) Search(query string, userID int, includeAll bool) ([]CachedResult, error) {
	q := `
		SELECT id, title, tracker, category, size, seeders, leechers, age, magnet_uri, link, info_hash, publish_date, saved_at
		FROM results
		WHERE LOWER(query) = LOWER(?) AND incognito = 0`
	args := []any{query}
	if !includeAll {
		q += " AND user_id = ?"
		args = append(args, userID)
	}
	q += " ORDER BY seeders DESC, saved_at DESC"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CachedResult
	for rows.Next() {
		var r CachedResult
		var savedAt string
		if err := rows.Scan(
			&r.ID,
			&r.Title, &r.Tracker, &r.Category, &r.Size,
			&r.Seeders, &r.Leechers, &r.Age, &r.MagnetURI,
			&r.Link, &r.InfoHash, &r.PublishDate, &savedAt,
		); err != nil {
			continue
		}
		r.SavedAt= dbutil.ParseTime(savedAt)
		r.Cached = true
		out = append(out, r)
	}
	return out, rows.Err()
}

// SearchAll runs an FTS5 full-text search across all cached results.
// Filters by userID unless includeAll=true (admin override).
func (s *Store) SearchAll(query string, limit int, userID int, includeAll bool) ([]CachedResult, error) {
	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return []CachedResult{}, nil
	}
	if limit <= 0 {
		limit = 200
	}
	q := `
		SELECT r.id, r.title, r.tracker, r.category, r.size, r.seeders, r.leechers,
		       r.age, r.magnet_uri, r.link, r.info_hash, r.publish_date, r.saved_at, r.query
		FROM results_fts
		JOIN results r ON r.id = results_fts.rowid
		WHERE results_fts MATCH ? AND r.incognito = 0`
	args := []any{ftsQuery}
	if !includeAll {
		q += " AND r.user_id = ?"
		args = append(args, userID)
	}
	q += " ORDER BY rank LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CachedResult
	seenHash := make(map[string]bool)
	for rows.Next() {
		var r CachedResult
		var savedAt string
		if err := rows.Scan(
			&r.ID,
			&r.Title, &r.Tracker, &r.Category, &r.Size,
			&r.Seeders, &r.Leechers, &r.Age, &r.MagnetURI,
			&r.Link, &r.InfoHash, &r.PublishDate, &savedAt, &r.Query,
		); err != nil {
			continue
		}
		// Dedup by infoHash — same content may appear under multiple original queries
		if r.InfoHash != "" {
			if seenHash[r.InfoHash] {
				continue
			}
			seenHash[r.InfoHash] = true
		}
		r.SavedAt= dbutil.ParseTime(savedAt)
		r.Cached = true
		out = append(out, r)
	}
	return out, rows.Err()
}

// Entry holds metadata about a past search query.
type Entry struct {
	Query       string    `json:"query"`
	ResultCount int       `json:"resultCount"`
	LastSaved   time.Time `json:"lastSaved"`
}

// RecentEntries returns the N most recently searched queries with metadata.
// Filters by userID unless includeAll=true (admin override).
func (s *Store) RecentEntries(limit int, userID int, includeAll bool) ([]Entry, error) {
	q := `
		SELECT query, COUNT(*) AS result_count, MAX(saved_at) AS last_seen
		FROM results
		WHERE incognito = 0`
	args := []any{}
	if !includeAll {
		q += " AND user_id = ?"
		args = append(args, userID)
	}
	q += `
		GROUP BY query
		ORDER BY last_seen DESC
		LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var e Entry
		var lastSeen string
		if err := rows.Scan(&e.Query, &e.ResultCount, &lastSeen); err != nil {
			continue
		}
		e.LastSaved= dbutil.ParseTime(lastSeen)
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetResult fetches one cached row by primary key. Returns sql.ErrNoRows when
// the row doesn't exist or the user doesn't own it (and isn't an admin
// requesting cross-user access). Used by the refresh endpoint to look up the
// original title + info_hash before talking to Jackett.
func (s *Store) GetResult(id int64, userID int, isAdmin bool) (*CachedResult, error) {
	q := `
		SELECT id, title, tracker, category, size, seeders, leechers, age, magnet_uri, link, info_hash, publish_date, saved_at, query
		FROM results
		WHERE id = ?`
	args := []any{id}
	if !isAdmin {
		q += " AND user_id = ?"
		args = append(args, userID)
	}
	row := s.db.QueryRow(q, args...)
	var r CachedResult
	var savedAt string
	if err := row.Scan(
		&r.ID,
		&r.Title, &r.Tracker, &r.Category, &r.Size,
		&r.Seeders, &r.Leechers, &r.Age, &r.MagnetURI,
		&r.Link, &r.InfoHash, &r.PublishDate, &savedAt, &r.Query,
	); err != nil {
		return nil, err
	}
	r.SavedAt = dbutil.ParseTime(savedAt)
	r.Cached = true
	return &r, nil
}

// UpdateSeedersLeechers refreshes the swarm counters for a cached row and
// bumps saved_at to "now" so subsequent UI sorts treat the row as fresh.
// Used by the refresh endpoint after a fresh Jackett poll.
func (s *Store) UpdateSeedersLeechers(id int64, seeders, leechers int) error {
	_, err := s.db.Exec(
		`UPDATE results SET seeders = ?, leechers = ?, saved_at = CURRENT_TIMESTAMP WHERE id = ?`,
		seeders, leechers, id,
	)
	return err
}

// Cleanup removes results older than the given duration.
func (s *Store) Cleanup(olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan).Format(dbutil.TimeFormat)
	_, err := s.db.Exec("DELETE FROM results WHERE saved_at < ?", cutoff)
	return err
}

// DeleteQuery removes cached results for a specific query.
// If includeAll=true, deletes regardless of owner (admin). Otherwise only user's rows.
func (s *Store) DeleteQuery(query string, userID int, includeAll bool) error {
	if includeAll {
		_, err := s.db.Exec("DELETE FROM results WHERE LOWER(query) = LOWER(?)", query)
		return err
	}
	_, err := s.db.Exec("DELETE FROM results WHERE LOWER(query) = LOWER(?) AND user_id = ?", query, userID)
	return err
}

// DeleteAll removes the user's cached results. Admin (includeAll) wipes everyone.
func (s *Store) DeleteAll(userID int, includeAll bool) error {
	if includeAll {
		_, err := s.db.Exec("DELETE FROM results")
		return err
	}
	_, err := s.db.Exec("DELETE FROM results WHERE user_id = ?", userID)
	return err
}
