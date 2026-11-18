package history

import (
	"context"
	"database/sql"
	"strings"

	"github.com/lgldsilva/jackui/internal/jackett"
)

// saveTx is the slice of *dbutil.Tx that Save needs; a seam so tests can count
// the statements one Save issues.
type saveTx interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Save persists Jackett results for a query, attributed to a user.
// Dedup is per-(user, info_hash) so two users searching the same content each get their own row.
// userID=0 means "no auth" (legacy/anonymous mode).
// When incognito=true the row is written with incognito=1 so it is visible during
// the current session but excluded from normal queries and deleted on session end.
func (s *Store) Save(ctx context.Context, query string, results []jackett.Result, userID int, incognito bool) error {
	if len(results) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	incognitoVal := 0
	if incognito {
		incognitoVal = 1
	}
	if err := saveResults(ctx, tx, strings.ToLower(query), results, userID, incognitoVal); err != nil {
		return err
	}
	return tx.Commit()
}

// saveChunkRows bounds one multi-row INSERT: 14 bind parameters per row keeps
// a chunk far below PostgreSQL's 65535-parameter limit.
const saveChunkRows = 500

const saveColumns = 14

// saveResults stores the batch with a constant number of statements — one
// dedup lookup plus one multi-row INSERT per chunk — instead of a COUNT and an
// INSERT per result. Search runs it synchronously before the SSE `done` event.
func saveResults(ctx context.Context, tx saveTx, lowerQuery string, results []jackett.Result, userID, incognitoVal int) error {
	stored, err := storedHashes(ctx, tx, results, userID, incognitoVal)
	if err != nil {
		return err
	}
	fresh := dedupResults(results, stored)
	for start := 0; start < len(fresh); start += saveChunkRows {
		end := min(start+saveChunkRows, len(fresh))
		if err := insertResults(ctx, tx, lowerQuery, fresh[start:end], userID, incognitoVal); err != nil {
			return err
		}
	}
	return nil
}

// storedHashes returns which of the batch's info hashes this (user, incognito)
// lane already has. A batch without hashes costs no query.
func storedHashes(ctx context.Context, tx saveTx, results []jackett.Result, userID, incognitoVal int) (map[string]bool, error) {
	hashes := make([]string, 0, len(results))
	for _, r := range results {
		if r.InfoHash != "" {
			hashes = append(hashes, r.InfoHash)
		}
	}
	stored := make(map[string]bool)
	if len(hashes) == 0 {
		return stored, nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT DISTINCT info_hash FROM results WHERE user_id = ? AND incognito = ? AND info_hash = ANY(?)", userID, incognitoVal, hashes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		stored[h] = true
	}
	return stored, rows.Err()
}

// dedupResults keeps the first occurrence of each info hash that is not
// already stored. Hash-less rows are always kept. Arrival order is preserved.
func dedupResults(results []jackett.Result, stored map[string]bool) []jackett.Result {
	fresh := make([]jackett.Result, 0, len(results))
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		if r.InfoHash != "" {
			if seen[r.InfoHash] || stored[r.InfoHash] {
				continue
			}
			seen[r.InfoHash] = true
		}
		fresh = append(fresh, r)
	}
	return fresh
}

func insertResults(ctx context.Context, tx saveTx, lowerQuery string, rows []jackett.Result, userID, incognitoVal int) error {
	if len(rows) == 0 {
		return nil
	}
	const tuple = "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	var q strings.Builder
	q.WriteString("INSERT INTO results (query, title, tracker, category, size, seeders, leechers, age, magnet_uri, link, info_hash, publish_date, user_id, incognito) VALUES ")
	args := make([]any, 0, len(rows)*saveColumns)
	for i, r := range rows {
		if i > 0 {
			q.WriteString(", ")
		}
		q.WriteString(tuple)
		args = append(args, lowerQuery, r.Title, r.Tracker, r.Category, r.Size, r.Seeders, r.Leechers, r.Age, r.MagnetURI, r.Link, r.InfoHash, r.PublishDate, userID, incognitoVal)
	}
	_, err := tx.ExecContext(ctx, q.String(), args...)
	return err
}
