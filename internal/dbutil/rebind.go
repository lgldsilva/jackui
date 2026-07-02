package dbutil

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// Rebind rewrites positional `?` placeholders into PostgreSQL's `$1, $2, …`
// form. Stores keep writing queries with `?` (the historical SQLite style,
// and the only form that works for dynamically-built `IN (?,?,…)` clauses);
// the Postgres wrappers below call Rebind at the boundary so the SQL the
// driver sees is dialect-correct.
//
// `?` inside single-quoted string literals is left untouched. The scan toggles
// an in-string flag on every `'`; an escaped `”` inside a literal toggles
// twice, so the flag stays correct. The project has no `?` inside SQL literals
// today, but the guard keeps Rebind safe if one is ever added.
func Rebind(query string) string {
	if !strings.ContainsRune(query, '?') {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	arg := 0
	inString := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '\'':
			inString = !inString
			b.WriteByte(c)
		case c == '?' && !inString:
			arg++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(arg))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// DB wraps *sql.DB and rebinds every query to the Postgres placeholder style.
// Stores swap their `db *sql.DB` field for `db *dbutil.DB`; the method set
// mirrors database/sql so call sites are unchanged apart from the field type.
type DB struct{ *sql.DB }

// Wrap adapts a raw *sql.DB (the shared Postgres pool) into a rebinding DB.
func Wrap(db *sql.DB) *DB { return &DB{db} }

// Unwrap exposes the underlying pool (for callers that need the raw handle,
// e.g. passing it to a sub-store or closing it once at shutdown).
func (d *DB) Unwrap() *sql.DB { return d.DB }

func (d *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return d.DB.Query(Rebind(query), args...)
}

func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(ctx, Rebind(query), args...)
}

func (d *DB) QueryRow(query string, args ...any) *sql.Row {
	return d.DB.QueryRow(Rebind(query), args...)
}

func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.DB.QueryRowContext(ctx, Rebind(query), args...)
}

func (d *DB) Exec(query string, args ...any) (sql.Result, error) {
	return d.DB.Exec(Rebind(query), args...)
}

func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.DB.ExecContext(ctx, Rebind(query), args...)
}

func (d *DB) Prepare(query string) (*sql.Stmt, error) {
	return d.DB.Prepare(Rebind(query))
}

func (d *DB) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return d.DB.PrepareContext(ctx, Rebind(query))
}

// Begin starts a transaction whose statements are also rebound.
func (d *DB) Begin() (*Tx, error) {
	tx, err := d.DB.Begin()
	if err != nil {
		return nil, err
	}
	return &Tx{tx}, nil
}

func (d *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	tx, err := d.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &Tx{tx}, nil
}

// Tx wraps *sql.Tx with the same rebinding behaviour as DB.
type Tx struct{ *sql.Tx }

func (t *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	return t.Tx.Query(Rebind(query), args...)
}

func (t *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.Tx.QueryContext(ctx, Rebind(query), args...)
}

func (t *Tx) QueryRow(query string, args ...any) *sql.Row {
	return t.Tx.QueryRow(Rebind(query), args...)
}

func (t *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.Tx.QueryRowContext(ctx, Rebind(query), args...)
}

func (t *Tx) Exec(query string, args ...any) (sql.Result, error) {
	return t.Tx.Exec(Rebind(query), args...)
}

func (t *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.Tx.ExecContext(ctx, Rebind(query), args...)
}

func (t *Tx) Prepare(query string) (*sql.Stmt, error) {
	return t.Tx.Prepare(Rebind(query))
}

func (t *Tx) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return t.Tx.PrepareContext(ctx, Rebind(query))
}

// HasColumn reports whether a column exists on a table in the current schema.
// Postgres has no PRAGMA table_info; this queries information_schema. It is
// kept for any runtime column checks, though declarative migrations make the
// old idempotent ADD COLUMN dance unnecessary.
func HasColumn(db *sql.DB, table, col string) bool {
	var n int
	err := db.QueryRow(
		`SELECT 1 FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`,
		table, col).Scan(&n)
	return err == nil && n == 1
}
