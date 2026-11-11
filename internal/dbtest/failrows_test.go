package dbtest

import (
	"errors"
	"io"
	"testing"
)

func TestFailRowsDB_EveryQueryFailsAfterRows(t *testing.T) {
	f := NewFailRowsDB(t, [][]any{{"a", "b"}}, io.ErrUnexpectedEOF)
	rows, err := f.Query(`SELECT a, b`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if len(cols) != 2 || cols[0] != "c0" || cols[1] != "c1" {
		t.Fatalf("columns = %v, want [c0 c1]", cols)
	}
	if !rows.Next() {
		t.Fatal("expected the yielded row before the iteration error")
	}
	var a, b string
	if err := rows.Scan(&a, &b); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if a != "a" || b != "b" {
		t.Fatalf("row = %q %q, want a b", a, b)
	}
	if rows.Next() {
		t.Fatal("iteration should stop on the injected error")
	}
	if !errors.Is(rows.Err(), io.ErrUnexpectedEOF) {
		t.Fatalf("rows.Err = %v, want ErrUnexpectedEOF", rows.Err())
	}
	if _, err := f.Exec(`INSERT INTO t VALUES (1)`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if f.Execs() != 1 || f.Queries() != 1 {
		t.Fatalf("execs=%d queries=%d, want 1 and 1", f.Execs(), f.Queries())
	}
}

func TestFailRowsDB_EmptyResultStillErrors(t *testing.T) {
	f := NewFailRowsDB(t, nil, io.ErrUnexpectedEOF)
	rows, err := f.Query(`SELECT x`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if len(cols) != 0 {
		t.Fatalf("columns = %v, want none", cols)
	}
	if rows.Next() {
		t.Fatal("an empty result has no row to yield")
	}
	if !errors.Is(rows.Err(), io.ErrUnexpectedEOF) {
		t.Fatalf("rows.Err = %v, want ErrUnexpectedEOF", rows.Err())
	}
}

func TestFailRowsDBQueries_FailsOnlyTheMatchingQuery(t *testing.T) {
	f := NewFailRowsDBQueries(t,
		QueryResult{Match: "ranks", Rows: [][]any{{int64(10), int64(1)}}, Err: io.ErrUnexpectedEOF},
		QueryResult{Rows: [][]any{{"2026-W40"}}},           // first fallback wins
		QueryResult{Rows: [][]any{{"should-not-be-used"}}}, // later empty Match is ignored
	)

	var week string
	if err := f.QueryRow(`SELECT week_key FROM trending_snapshot`).Scan(&week); err != nil {
		t.Fatalf("week lookup: %v", err)
	}
	if week != "2026-W40" {
		t.Fatalf("week = %q, want the first fallback row", week)
	}

	rows, err := f.Query(`SELECT tmdb_id, ranks FROM trending_snapshot`)
	if err != nil {
		t.Fatalf("rank query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatal("expected the rank row before rows.Err")
	}
	var id, rank int
	if err := rows.Scan(&id, &rank); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if id != 10 || rank != 1 {
		t.Fatalf("rank row = %d,%d, want 10,1", id, rank)
	}
	if rows.Next() {
		t.Fatal("rank scan should stop on the injected error")
	}
	if !errors.Is(rows.Err(), io.ErrUnexpectedEOF) {
		t.Fatalf("rows.Err = %v, want ErrUnexpectedEOF", rows.Err())
	}
	if f.Queries() != 2 {
		t.Fatalf("queries = %d, want 2", f.Queries())
	}
}

func TestFailRowsDBQueries_NilErrEndsCleanly(t *testing.T) {
	f := NewFailRowsDBQueries(t, QueryResult{Rows: [][]any{{"only"}}})
	rows, err := f.Query(`SELECT x`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatal("expected the row")
	}
	var got string
	if err := rows.Scan(&got); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got != "only" {
		t.Fatalf("row = %q, want only", got)
	}
	if rows.Next() {
		t.Fatal("nil Err must end the iteration, not spin")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err = %v, want nil", err)
	}
}

func TestFailRowsDBQueries_UnknownQueryIsEmpty(t *testing.T) {
	f := NewFailRowsDBQueries(t, QueryResult{Match: "only-this", Rows: [][]any{{"x"}}})
	rows, err := f.Query(`SELECT other`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Fatal("an unmatched query has no rows")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err = %v, want nil", err)
	}
}

func TestFailRowsDB_TransactionAndDriverOpen(t *testing.T) {
	f := NewFailRowsDB(t, nil, io.ErrUnexpectedEOF)
	tx, err := f.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := tx.Exec(`DELETE FROM t`); err != nil {
		t.Fatalf("tx Exec: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	tx, err = f.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if f.Execs() != 1 {
		t.Fatalf("execs = %d, want 1", f.Execs())
	}
	drv := (&failRowsConnector{}).Driver()
	if _, err := drv.Open("dsn"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Open err = %v, want ErrUnexpectedEOF", err)
	}
}
