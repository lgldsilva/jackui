package subtitles

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file provides an in-memory database/sql fake so the AutoFetcher and
// LinksStore pipelines can be tested WITHOUT a PostgreSQL server. The
// pre-push coverage gate and PR CI run without JACKUI_TEST_DATABASE_URL
// (dbtest.NewDB skips), so the suite must reach the ≥90% floor with this fake;
// the DB-gated tests in links_test.go / auto_test.go re-run the same
// lifecycle against real Postgres when the variable IS set.
//
// The fake implements driver.QueryerContext/ExecerContext and routes on the
// (Rebind-rewritten) SQL shape the store issues, mirroring each statement's
// ON CONFLICT semantics. It is NOT a SQL engine: tests that need real
// semantics (upsert column preservation) assert the store's PARAMETERS
// through the recorded execs, while the SQL itself stays covered by the
// dbtest suite.

// linkRowColumns mirrors linkColumns order — links.go's Scan contract.
var linkRowColumns = []string{
	"id", "info_hash", "file_index", "status", "reason", "file_id", "language",
	"languages", "hearing_impaired", "forced", "vtt_path", "source",
	"hash_refined", "created_at", "updated_at",
}

// fakeExec records one Exec call (SQL + converted args) for assertions on
// exactly what the store persists.
type fakeExec struct {
	SQL  string
	Args []driver.Value
}

// fakeLinksDB is the shared state behind every conn opened from one
// newFakeLinksStore call. All methods are mutex-guarded: fetch goroutines
// write while the test goroutine reads.
type fakeLinksDB struct {
	mu     sync.Mutex
	rows   map[string]*SubtitleLink // key: "infoHash#fileIndex"
	nextID int64
	execs  []fakeExec

	qErr   error  // returned by every Query while set
	eErr   error  // returned by every Exec while set
	onExec func() // runs mid-Exec (after the state mutation), for tests that
	// must cancel a context BETWEEN a successful write and the caller's
	// next context check — database/sql refuses an Exec whose ctx is
	// already done, so the hook is the only deterministic window.
}

func newFakeLinksDB() *fakeLinksDB {
	return &fakeLinksDB{rows: make(map[string]*SubtitleLink), nextID: 100}
}

func linkKey(infoHash string, fileIndex int64) string {
	return fmt.Sprintf("%s#%d", infoHash, fileIndex)
}

// rowValues flattens a row into driver.Values in linkRowColumns order.
func rowValues(l *SubtitleLink) []driver.Value {
	return []driver.Value{
		l.ID, l.InfoHash, int64(l.FileIndex), l.Status, l.Reason, l.FileID,
		l.Language, l.Languages, l.HearingImpaired, l.Forced, l.VTTPath,
		l.Source, l.HashRefined, l.CreatedAt, l.UpdatedAt,
	}
}

// snapshot returns a deep copy of the row for (infoHash, fileIndex), nil when
// absent — tests read through this instead of racing the map directly.
func (f *fakeLinksDB) snapshot(infoHash string, fileIndex int64) *SubtitleLink {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.rows[linkKey(infoHash, fileIndex)]
	if row == nil {
		return nil
	}
	cp := *row
	return &cp
}

// recordedExecs returns a copy of the exec log.
func (f *fakeLinksDB) recordedExecs() []fakeExec {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeExec, len(f.execs))
	copy(out, f.execs)
	return out
}

func (f *fakeLinksDB) failQueries(err error) { f.mu.Lock(); f.qErr = err; f.mu.Unlock() }
func (f *fakeLinksDB) failExecs(err error)   { f.mu.Lock(); f.eErr = err; f.mu.Unlock() }
func (f *fakeLinksDB) clearFailures()        { f.mu.Lock(); f.qErr, f.eErr = nil, nil; f.mu.Unlock() }

// setExecHook installs a callback invoked inside every successful Exec, after
// the state mutation but before the result returns to database/sql.
func (f *fakeLinksDB) setExecHook(hook func()) { f.mu.Lock(); f.onExec = hook; f.mu.Unlock() }

// upsertRow implements the shared "create if absent, keep id/created_at"
// part of the store's ON CONFLICT clauses.
func (f *fakeLinksDB) upsertRow(infoHash string, fileIndex int64, now time.Time) *SubtitleLink {
	row := f.rows[linkKey(infoHash, fileIndex)]
	if row == nil {
		f.nextID++
		row = &SubtitleLink{
			ID:        f.nextID,
			InfoHash:  infoHash,
			FileIndex: int(fileIndex),
			CreatedAt: now,
		}
		f.rows[linkKey(infoHash, fileIndex)] = row
	}
	row.UpdatedAt = now
	return row
}

// fakeDriver / fakeConnector wire the fake into database/sql. OpenDB needs no
// global registration, so concurrent tests never collide on driver names.
type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) {
	return nil, fmt.Errorf("subtitles fake driver: use sql.OpenDB")
}

type fakeConnector struct{ db *fakeLinksDB }

func (c fakeConnector) Connect(context.Context) (driver.Conn, error) {
	return &fakeConn{db: c.db}, nil
}

func (c fakeConnector) Driver() driver.Driver { return fakeDriver{} }

// fakeConn handles the store's Query/Exec traffic. The store never prepares
// statements or begins transactions, so those interfaces are unsupported.
type fakeConn struct{ db *fakeLinksDB }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("subtitles fake driver: prepared statements unsupported")
}
func (c *fakeConn) Close() error              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) { return nil, fmt.Errorf("transactions unsupported") }

// Query handles LinksStore.Get: one row (or none) for the key args.
func (c *fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.db.mu.Lock()
	defer c.db.mu.Unlock()
	if c.db.qErr != nil {
		return nil, c.db.qErr
	}
	if !strings.HasPrefix(strings.TrimSpace(query), "SELECT") || len(args) != 2 {
		return nil, fmt.Errorf("subtitles fake driver: unexpected query %q (%d args)", query, len(args))
	}
	rows := &fakeRows{cols: linkRowColumns}
	row := c.db.rows[linkKey(asString(args[0].Value), asInt64(args[1].Value))]
	if row != nil {
		rows.vals = append(rows.vals, rowValues(row))
	}
	return rows, nil
}

// Exec handles UpsertPending / MarkReady / MarkRefined / MarkFailed,
// discriminated by SQL shape and arity exactly as the store issues them.
func (c *fakeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.db.mu.Lock()
	defer c.db.mu.Unlock()
	if c.db.eErr != nil {
		return nil, c.db.eErr
	}
	c.db.execs = append(c.db.execs, fakeExec{SQL: strings.TrimSpace(query), Args: namedValues(args)})

	now := time.Now()
	q := strings.TrimSpace(query)
	var (
		res driver.Result
		err error
	)
	switch {
	case strings.HasPrefix(q, "SELECT"):
		err = fmt.Errorf("subtitles fake driver: SELECT via Exec: %q", query)
	case strings.HasPrefix(q, "UPDATE"):
		// MarkRefined: hash_refined on the EXISTING row only — no upsert.
		row := c.db.rows[linkKey(asString(args[0].Value), asInt64(args[1].Value))]
		if row == nil {
			res = driver.RowsAffected(0)
			break
		}
		row.HashRefined = true
		row.UpdatedAt = now
		res = driver.RowsAffected(1)
	case strings.HasPrefix(q, "INSERT") && len(args) == 11:
		// MarkReady: reason is the inlined literal '' (not a placeholder), so
		// the placeholders are info_hash, file_index, status, file_id,
		// language, languages, hearing_impaired, forced, vtt_path, source,
		// hash_refined.
		row := c.db.upsertRow(asString(args[0].Value), asInt64(args[1].Value), now)
		row.Status = asString(args[2].Value)
		row.Reason = ""
		row.FileID = asString(args[3].Value)
		row.Language = asString(args[4].Value)
		row.Languages = asString(args[5].Value)
		row.HearingImpaired = asBool(args[6].Value)
		row.Forced = asBool(args[7].Value)
		row.VTTPath = asString(args[8].Value)
		row.Source = asString(args[9].Value)
		row.HashRefined = asBool(args[10].Value)
		res = driver.RowsAffected(1)
	case strings.HasPrefix(q, "INSERT") && len(args) == 4:
		// UpsertPending (status=pending: reason cleared, languages set) or
		// MarkFailed (status=failed: reason set, outcome columns untouched).
		row := c.db.upsertRow(asString(args[0].Value), asInt64(args[1].Value), now)
		row.Status = asString(args[2].Value)
		if asString(args[2].Value) == StatusPending {
			row.Reason = ""
			row.Languages = asString(args[3].Value)
		} else {
			row.Reason = asString(args[3].Value)
		}
		res = driver.RowsAffected(1)
	default:
		err = fmt.Errorf("subtitles fake driver: unexpected exec %q (%d args)", query, len(args))
	}
	// The hook runs while still holding the fake's lock: it must not re-enter
	// the fake. It exists to cancel a caller's context inside the narrow
	// window between a successful write and the caller's next ctx check.
	if err == nil && c.db.onExec != nil {
		c.db.onExec()
	}
	return res, err
}

// fakeRows is a fully-materialized in-memory result set.
type fakeRows struct {
	cols []string
	vals [][]driver.Value
	pos  int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.vals) {
		return io.EOF // database/sql's documented end-of-rows signal
	}
	copy(dest, r.vals[r.pos])
	r.pos++
	return nil
}

func namedValues(args []driver.NamedValue) []driver.Value {
	out := make([]driver.Value, len(args))
	for i, a := range args {
		out[i] = a.Value
	}
	return out
}

func asString(v driver.Value) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func asInt64(v driver.Value) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}

func asBool(v driver.Value) bool {
	b, _ := v.(bool)
	return b
}

// newFakeLinksStore builds a LinksStore over a fresh fakeLinksDB.
func newFakeLinksStore(t *testing.T) (*LinksStore, *fakeLinksDB) {
	t.Helper()
	fdb := newFakeLinksDB()
	pool := sql.OpenDB(fakeConnector{db: fdb})
	t.Cleanup(func() { _ = pool.Close() })
	s, err := NewLinksStore(pool)
	if err != nil {
		t.Fatalf("NewLinksStore(fake): %v", err)
	}
	return s, fdb
}
