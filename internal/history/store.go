package history

import (
	"database/sql"
	"strings"
	"time"
	"unicode"

	"github.com/lgldsilva/jackui/internal/dbutil"
	"github.com/lgldsilva/jackui/internal/jackett"
)

const queryUserClause = " AND user_id = ?"

// MaxSearchResults caps history reads for SSE cache/convergence and list APIs.
// Without a limit a popular query with thousands of cached rows scans the full
// table twice per SSE connection.
const MaxSearchResults = 500

type Store struct {
	db *dbutil.DB
}

type CachedResult struct {
	jackett.Result
	ID      int64     `json:"id,omitempty"`
	SavedAt time.Time `json:"savedAt"`
	Cached  bool      `json:"cached"`
	Query   string    `json:"query,omitempty"` // populated by SearchAll to show origin
}

// New wires the history store onto the shared Postgres pool. Schema is applied
// centrally (internal/db migrations).
func New(pool *sql.DB) (*Store, error) {
	return &Store{db: dbutil.Wrap(pool)}, nil
}

// Close is a no-op: the shared pool's lifecycle is owned by main.
func (s *Store) Close() {
	// No-op: shared Postgres pool lifecycle is owned by main (S1186).
}

// buildFTSQuery turns a user query into a Postgres to_tsquery prefix-AND string.
// Each token becomes `tok:*` joined by ` & `, reproducing the SQLite FTS5
// prefix-AND behaviour. Tokens are reduced to letters/digits so the to_tsquery
// parser never sees its operators (& | ! ( ) : *) — diacritics are stripped on
// both sides by immutable_unaccent in SQL. Example: `breaking bad` -> `breaking:* & bad:*`.
func buildFTSQuery(input string) string {
	fields := strings.Fields(input)
	tokens := make([]string, 0, len(fields))
	for _, f := range fields {
		f = sanitizeTsToken(f)
		if f == "" {
			continue
		}
		tokens = append(tokens, f+":*")
	}
	return strings.Join(tokens, " & ")
}

// sanitizeTsToken lower-cases a token and keeps only letters/digits, dropping
// every character to_tsquery would treat as an operator.
func sanitizeTsToken(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
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

	lowerQuery := strings.ToLower(query)
	for _, r := range results {
		if skip, err := historyRowExists(tx, r, seen, userID, incognitoVal); err != nil {
			return err
		} else if skip {
			continue
		}
		_, err := tx.Exec(`
			INSERT INTO results (query, title, tracker, category, size, seeders, leechers, age, magnet_uri, link, info_hash, publish_date, user_id, incognito)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, lowerQuery, r.Title, r.Tracker, r.Category, r.Size, r.Seeders, r.Leechers, r.Age, r.MagnetURI, r.Link, r.InfoHash, r.PublishDate, userID, incognitoVal)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// historyRowExists dedups by info_hash: in-batch via `seen`, and against
// already-persisted rows via a COUNT. Returns skip=true when the result
// should not be inserted. InfoHash-less rows are always inserted.
func historyRowExists(tx *dbutil.Tx, r jackett.Result, seen map[string]bool, userID, incognitoVal int) (bool, error) {
	if r.InfoHash == "" {
		return false, nil
	}
	if seen[r.InfoHash] {
		return true, nil
	}
	seen[r.InfoHash] = true

	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM results WHERE info_hash = ? AND user_id = ? AND incognito = ?", r.InfoHash, userID, incognitoVal).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// DistinctQueryCount returns how many different searches the user has saved
// (incognito excluded) — feeds the stats endpoint.
func (s *Store) DistinctQueryCount(userID int) (int, error) {
	var n int
	err := s.db.QueryRow(`
		SELECT COUNT(DISTINCT LOWER(query)) FROM results WHERE user_id = ? AND incognito = 0
	`, userID).Scan(&n)
	return n, err
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

// DeleteAllIncognito removes every incognito-flagged entry across all users.
// Called once at startup: after a restart the in-memory heartbeat map (which
// the reaper relies on) is empty, so any incognito row left in the DB is
// orphaned and would persist forever — defeating the purpose of incognito.
func (s *Store) DeleteAllIncognito() error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM results WHERE incognito = 1`)
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
		q += queryUserClause
		args = append(args, userID)
	}
	q += " ORDER BY seeders DESC, saved_at DESC LIMIT ?"
	args = append(args, MaxSearchResults)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CachedResult
	for rows.Next() {
		var r CachedResult
		if err := rows.Scan(
			&r.ID,
			&r.Title, &r.Tracker, &r.Category, &r.Size,
			&r.Seeders, &r.Leechers, &r.Age, &r.MagnetURI,
			&r.Link, &r.InfoHash, &r.PublishDate, &r.SavedAt,
		); err != nil {
			continue
		}
		r.Cached = true
		out = append(out, r)
	}
	return out, rows.Err()
}

// SearchAll runs a Postgres full-text search across all cached results.
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
		FROM results r
		WHERE r.fts @@ to_tsquery('simple', public.immutable_unaccent(?)) AND r.incognito = 0`
	args := []any{ftsQuery}
	if !includeAll {
		q += " AND r.user_id = ?"
		args = append(args, userID)
	}
	// ts_rank DESC (higher = better) replaces FTS5's `rank` (lower = better, ASC).
	q += " ORDER BY ts_rank(r.fts, to_tsquery('simple', public.immutable_unaccent(?))) DESC LIMIT ?"
	args = append(args, ftsQuery, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CachedResult
	seenHash := make(map[string]bool)
	for rows.Next() {
		var r CachedResult
		if err := rows.Scan(
			&r.ID,
			&r.Title, &r.Tracker, &r.Category, &r.Size,
			&r.Seeders, &r.Leechers, &r.Age, &r.MagnetURI,
			&r.Link, &r.InfoHash, &r.PublishDate, &r.SavedAt, &r.Query,
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
		q += queryUserClause
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
		if err := rows.Scan(&e.Query, &e.ResultCount, &e.LastSaved); err != nil {
			continue
		}
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
		q += queryUserClause
		args = append(args, userID)
	}
	row := s.db.QueryRow(q, args...)
	var r CachedResult
	if err := row.Scan(
		&r.ID,
		&r.Title, &r.Tracker, &r.Category, &r.Size,
		&r.Seeders, &r.Leechers, &r.Age, &r.MagnetURI,
		&r.Link, &r.InfoHash, &r.PublishDate, &r.SavedAt, &r.Query,
	); err != nil {
		return nil, err
	}
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
	cutoff := time.Now().Add(-olderThan)
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
