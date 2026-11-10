package dbtest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strconv"
	"sync/atomic"
	"testing"
)

// FailRowsDB is a *sql.DB whose every query yields Rows, then fails the
// iteration with Err — what a dropped connection mid-result looks like. Execs
// succeed and are counted, so a test can assert that a store did NOT write
// after a failed read (e.g. "seed defaults because nothing was read").
//
// It needs no Postgres, so these tests run everywhere (unlike NewDB).
type FailRowsDB struct {
	*sql.DB
	execs atomic.Int64
}

// Execs reports how many Exec calls reached the driver.
func (f *FailRowsDB) Execs() int64 { return f.execs.Load() }

// NewFailRowsDB returns a FailRowsDB yielding rows (each row's values in
// column order; columns are named c0..cN) before failing with err.
func NewFailRowsDB(t *testing.T, rows [][]any, err error) *FailRowsDB {
	t.Helper()
	f := &FailRowsDB{}
	c := &failRowsConnector{rows: rows, err: err, execs: &f.execs}
	f.DB = sql.OpenDB(c)
	t.Cleanup(func() { _ = f.DB.Close() })
	return f
}

type failRowsConnector struct {
	rows  [][]any
	err   error
	execs *atomic.Int64
}

func (c *failRowsConnector) Connect(context.Context) (driver.Conn, error) {
	return failRowsConn{c}, nil
}
func (c *failRowsConnector) Driver() driver.Driver { return failRowsDriver{} }

type failRowsDriver struct{}

func (failRowsDriver) Open(string) (driver.Conn, error) { return nil, io.ErrUnexpectedEOF }

type failRowsConn struct{ c *failRowsConnector }

func (f failRowsConn) Prepare(string) (driver.Stmt, error) { return failRowsStmt(f), nil }
func (failRowsConn) Close() error                          { return nil }
func (failRowsConn) Begin() (driver.Tx, error)             { return failRowsTx{}, nil }

type failRowsTx struct{}

func (failRowsTx) Commit() error   { return nil }
func (failRowsTx) Rollback() error { return nil }

type failRowsStmt struct{ c *failRowsConnector }

func (failRowsStmt) Close() error  { return nil }
func (failRowsStmt) NumInput() int { return -1 }
func (s failRowsStmt) Exec([]driver.Value) (driver.Result, error) {
	s.c.execs.Add(1)
	return driver.RowsAffected(1), nil
}
func (s failRowsStmt) Query([]driver.Value) (driver.Rows, error) {
	return &failRows{rows: s.c.rows, err: s.c.err}, nil
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
		return r.err
	}
	for j, v := range r.rows[r.i] {
		dest[j] = v
	}
	r.i++
	return nil
}
