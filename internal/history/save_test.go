package history

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/lgldsilva/jackui/internal/jackett"
)

// countingTx counts the statements Save sends through the transaction.
type countingTx struct {
	saveTx
	statements int
	failQuery  error
	failExec   error
}

func (c *countingTx) Query(query string, args ...any) (*sql.Rows, error) {
	c.statements++
	if c.failQuery != nil {
		return nil, c.failQuery
	}
	return c.saveTx.Query(query, args...)
}

func (c *countingTx) Exec(query string, args ...any) (sql.Result, error) {
	c.statements++
	if c.failExec != nil {
		return nil, c.failExec
	}
	return c.saveTx.Exec(query, args...)
}

func manyResults(n int) []jackett.Result {
	out := make([]jackett.Result, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, jackett.Result{Title: fmt.Sprintf("T%d", i), InfoHash: fmt.Sprintf("hash%03d", i), Seeders: i})
	}
	return out
}

// Save used to issue one COUNT plus one INSERT per result (2N statements,
// synchronously before the SSE `done` event). It must stay O(1) statements.
func TestSaveStatementCountIsConstant(t *testing.T) {
	s := newTestStore(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	ctx := &countingTx{saveTx: tx}
	if err := saveResults(ctx, "q", manyResults(50), 1, 0); err != nil {
		t.Fatalf("saveResults: %v", err)
	}
	if ctx.statements > 2 {
		t.Fatalf("saving 50 results issued %d statements, want <= 2 (one dedup lookup + one insert)", ctx.statements)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Search("q", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 {
		t.Fatalf("stored %d rows, want 50", len(got))
	}
}

// The batched path keeps Save's dedup rules: once per info_hash inside the
// batch, once per (user, incognito) against stored rows, and hash-less rows
// always inserted.
func TestSaveBatchedDedupSemantics(t *testing.T) {
	s := newTestStore(t)
	first := []jackett.Result{
		{Title: "A", InfoHash: "h1"},
		{Title: "A dup", InfoHash: "h1"},
		{Title: "no hash 1"},
		{Title: "no hash 2"},
	}
	if err := s.Save("q", first, 1, false); err != nil {
		t.Fatal(err)
	}
	second := []jackett.Result{
		{Title: "A again", InfoHash: "h1"},
		{Title: "B", InfoHash: "h2"},
		{Title: "no hash 3"},
	}
	if err := s.Save("q", second, 1, false); err != nil {
		t.Fatal(err)
	}
	// Another user and the incognito lane each keep their own copy of h1.
	if err := s.Save("q", []jackett.Result{{Title: "A other", InfoHash: "h1"}}, 2, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("q", []jackett.Result{{Title: "A incog", InfoHash: "h1"}}, 1, true); err != nil {
		t.Fatal(err)
	}

	count := func(where string, args ...any) int {
		t.Helper()
		var n int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM results WHERE "+where, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("user_id = 1 AND incognito = 0"); n != 5 {
		t.Errorf("user 1 rows = %d, want 5 (h1, h2, three hash-less)", n)
	}
	if n := count("user_id = 1 AND incognito = 0 AND info_hash = 'h1'"); n != 1 {
		t.Errorf("user 1 h1 rows = %d, want 1", n)
	}
	if n := count("user_id = 1 AND incognito = 0 AND title = 'A'"); n != 1 {
		t.Errorf("first occurrence of h1 must be the stored one, got %d rows titled A", n)
	}
	if n := count("user_id = 2 AND info_hash = 'h1'"); n != 1 {
		t.Errorf("user 2 h1 rows = %d, want 1", n)
	}
	if n := count("user_id = 1 AND incognito = 1 AND info_hash = 'h1'"); n != 1 {
		t.Errorf("incognito h1 rows = %d, want 1", n)
	}
}

// Rows keep their arrival order (ids ascending) and a batch larger than one
// INSERT chunk is stored completely.
func TestSaveChunksLargeBatchesInOrder(t *testing.T) {
	s := newTestStore(t)
	n := saveChunkRows*2 + 7
	if err := s.Save("big", manyResults(n), 1, false); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query("SELECT title FROM results WHERE user_id = 1 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("T%d", i); title != want {
			t.Fatalf("row %d title = %q, want %q", i, title, want)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if i != n {
		t.Fatalf("stored %d rows, want %d", i, n)
	}
}

func TestSaveResultsPropagatesErrors(t *testing.T) {
	s := newTestStore(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	boom := errors.New("boom")
	if err := saveResults(&countingTx{saveTx: tx, failQuery: boom}, "q", manyResults(3), 1, 0); !errors.Is(err, boom) {
		t.Errorf("dedup lookup error = %v, want boom", err)
	}
	if err := saveResults(&countingTx{saveTx: tx, failExec: boom}, "q", manyResults(3), 1, 0); !errors.Is(err, boom) {
		t.Errorf("insert error = %v, want boom", err)
	}
	// Hash-less only: no dedup lookup at all, straight to the insert.
	c := &countingTx{saveTx: tx}
	if err := saveResults(c, "q", []jackett.Result{{Title: "x"}}, 1, 0); err != nil {
		t.Fatal(err)
	}
	if c.statements != 1 {
		t.Errorf("hash-less batch issued %d statements, want 1", c.statements)
	}
}

func TestSaveOnClosedPoolFails(t *testing.T) {
	s := newTestStore(t)
	_ = s.db.Close()
	if err := s.Save("q", manyResults(1), 1, false); err == nil {
		t.Fatal("Save on a closed pool must fail")
	}
}
