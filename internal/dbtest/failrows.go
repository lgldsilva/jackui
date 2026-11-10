package dbtest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// FailRowsDB is a *sql.DB whose queries yield Rows, then fail the iteration
// with Err — what a dropped connection mid-result looks like. Execs succeed
// and are counted, so a test can assert that a store did NOT write after a
// failed read (e.g. "seed defaults because nothing was read").
//
// It needs no Postgres, so these tests run everywhere (unlike NewDB).
type FailRowsDB struct {
	*sql.DB
	execs   atomic.Int64
	queries atomic.Int64
}

// Execs reports how many Exec calls reached the driver.
func (f *FailRowsDB) Execs() int64 { return f.execs.Load() }

// Queries reports how many Query calls reached the driver.
func (f *FailRowsDB) Queries() int64 { return f.queries.Load() }

// QueryResult is the iteration one class of query produces. Match is a
// substring of the SQL the driver receives (dbutil.Rebind has already turned
// `?` into `$1`, `$2`, …). The first result whose Match is contained in the
// SQL wins; an empty Match is the fallback for every other query. A non-nil
// Err is what database/sql reports from rows.Err after Rows are consumed. A
// nil Err ends the iteration cleanly.
type QueryResult struct {
	Match string
	Rows  [][]any
	Err   error
}

// NewFailRowsDB returns a FailRowsDB yielding rows (each row's values in
// column order; columns are named c0..cN) before failing with err. Every
// query gets that same result.
func NewFailRowsDB(t *testing.T, rows [][]any, err error) *FailRowsDB {
	t.Helper()
	return newFailRowsDB(t, []QueryResult{{Rows: rows, Err: err}})
}

// NewFailRowsDBQueries is NewFailRowsDB with a different result per query.
// Use it when one function issues several queries and only one of them should
// fail mid-iteration (the others still need their own row shape).
func NewFailRowsDBQueries(t *testing.T, results ...QueryResult) *FailRowsDB {
	t.Helper()
	return newFailRowsDB(t, results)
}

func newFailRowsDB(t *testing.T, results []QueryResult) *FailRowsDB {
	t.Helper()
	f := &FailRowsDB{}
	c := &failRowsConnector{results: results, execs: &f.execs, queries: &f.queries}
	f.DB = sql.OpenDB(c)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

type failRowsConnector struct {
	results []QueryResult
	execs   *atomic.Int64
	queries *atomic.Int64
}

// resultFor picks the first specific match, then the first empty-Match
// fallback. No match and no fallback is an empty, successful iteration.
func (c *failRowsConnector) resultFor(query string) QueryResult {
	var fallback *QueryResult
	for i := range c.results {
		r := &c.results[i]
		if r.Match == "" {
			if fallback == nil {
				fallback = r
			}
			continue
		}
		if strings.Contains(query, r.Match) {
			return *r
		}
	}
	if fallback != nil {
		return *fallback
	}
	return QueryResult{}
}

func (c *failRowsConnector) Connect(context.Context) (driver.Conn, error) {
	return failRowsConn{c}, nil
}
func (c *failRowsConnector) Driver() driver.Driver { return failRowsDriver{} }

type failRowsDriver struct{}

func (failRowsDriver) Open(string) (driver.Conn, error) { return nil, io.ErrUnexpectedEOF }

type failRowsConn struct{ c *failRowsConnector }

func (f failRowsConn) Prepare(query string) (driver.Stmt, error) {
	return failRowsStmt{c: f.c, query: query}, nil
}
func (failRowsConn) Close() error              { return nil }
func (failRowsConn) Begin() (driver.Tx, error) { return failRowsTx{}, nil }

type failRowsTx struct{}

func (failRowsTx) Commit() error   { return nil }
func (failRowsTx) Rollback() error { return nil }

type failRowsStmt struct {
	c     *failRowsConnector
	query string
}

func (failRowsStmt) Close() error  { return nil }
func (failRowsStmt) NumInput() int { return -1 }
func (s failRowsStmt) Exec([]driver.Value) (driver.Result, error) {
	s.c.execs.Add(1)
	return driver.RowsAffected(1), nil
}
func (s failRowsStmt) Query([]driver.Value) (driver.Rows, error) {
	s.c.queries.Add(1)
	r := s.c.resultFor(s.query)
	return &failRows{rows: r.Rows, err: r.Err}, nil
}

type failRows struct {
	rows [][]any
	err  error
	i    int
}

func (r *failRows) Columns() []string {
	n := 0
	if len(r.rows) > 0 {
		n = len(r.rows[0])
	}
	cols := make([]string, n)
	for i := range cols {
		cols[i] = "c" + strconv.Itoa(i)
	}
	return cols
}

func (r *failRows) Close() error { return nil }

func (r *failRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		if r.err != nil {
			return r.err
		}
		return io.EOF
	}
	for j, v := range r.rows[r.i] {
		dest[j] = v
	}
	r.i++
	return nil
}
