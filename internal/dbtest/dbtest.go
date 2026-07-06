// Package dbtest provides an isolated PostgreSQL handle for store tests.
//
// Strategy (fast on slow disks): migrate ONE schema per test process, then make
// each test independent by TRUNCATEing its tables (milliseconds) instead of
// creating/migrating a fresh schema or database per test (hundreds of ms ×
// thousands of tests → package timeouts). No test uses t.Parallel(), so within a
// process tests run sequentially and the truncate-between-tests is safe; across
// processes each gets its own schema (keyed by pid).
//
//   - JACKUI_TEST_DATABASE_URL set → use that Postgres (the CI path).
//   - otherwise → t.Skip.
package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	appdb "github.com/lgldsilva/jackui/internal/db"
)

const (
	envURL      = "JACKUI_TEST_DATABASE_URL"
	advisoryKey = 913371
)

var (
	procOnce     sync.Once
	procErr      error
	procSchema   string
	truncateStmt string // cached "TRUNCATE a,b,... RESTART IDENTITY CASCADE"

	truncMu   sync.Mutex
	truncated = map[*testing.T]bool{}

	isoSeq atomic.Int64 // unique suffix for NewIsolatedDB schemas
)

// NewDB returns a *sql.DB scoped to this process's migrated schema. On the first
// call within a test it TRUNCATEs all tables (re-seeding the anonymous user), so
// each test starts clean. Returns a fresh handle each call (safe to Close in the
// "closed pool" error-path tests without affecting other handles). Skips when no
// test database is configured.
func NewDB(t *testing.T) *sql.DB {
	t.Helper()
	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("%s not set; skipping (needs a PostgreSQL test database)", envURL)
	}

	ensureProcessSchema(base)
	if procErr != nil {
		t.Fatalf("test schema init: %v", procErr)
	}

	pool, err := sql.Open("pgx", withSearchPath(base, procSchema))
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	pool.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = pool.Close() })

	// Clean the schema once per test (first NewDB call). Subsequent calls in the
	// same test reuse the already-clean schema, so a test that builds several
	// stores shares one DB (as in production) — only the closed-pool handle is
	// per-call.
	truncMu.Lock()
	first := !truncated[t]
	truncated[t] = true
	truncMu.Unlock()
	if first {
		t.Cleanup(func() {
			truncMu.Lock()
			delete(truncated, t)
			truncMu.Unlock()
		})
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if _, err := pool.ExecContext(ctx, truncateStmt); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		// Re-seed the anonymous sentinel user (id 0) that the schema migration
		// inserts and TRUNCATE just removed — FKs to users(id) depend on it.
		if _, err := pool.ExecContext(ctx,
			`INSERT INTO users(id, username, password_hash, role, status, email_verified)
			 VALUES (0, '', '', 'guest', 'disabled', 0) ON CONFLICT (id) DO NOTHING`); err != nil {
			t.Fatalf("reseed anon user: %v", err)
		}
	}

	return pool
}

// NewIsolatedDB returns a pool scoped to a PRIVATE, freshly-migrated schema,
// dropped on cleanup. Slower than NewDB (full migration per call) — use it only
// for the rare tests that mutate the schema itself (e.g. DROP TABLE to force a
// store error), which can't share NewDB's process-wide schema.
func NewIsolatedDB(t *testing.T) *sql.DB {
	t.Helper()
	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("%s not set; skipping (needs a PostgreSQL test database)", envURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	conn, err := admin.Conn(ctx)
	if err != nil {
		_ = admin.Close()
		t.Fatalf("admin conn: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", advisoryKey); err != nil {
		_ = conn.Close()
		_ = admin.Close()
		t.Fatalf("advisory lock: %v", err)
	}
	schema := fmt.Sprintf("dbtest_iso_p%d_%d", os.Getpid(), isoSeq.Add(1))
	mkErr := func() error {
		if err := createGlobals(ctx, conn); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schema)); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %q", schema))
		return err
	}()
	pool, mig := (*sql.DB)(nil), error(nil)
	if mkErr == nil {
		pool, mig = sql.Open("pgx", withSearchPath(base, schema))
		if mig == nil {
			mig = appdb.Migrate(pool)
		}
	}
	_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", advisoryKey)
	_ = conn.Close()
	_ = admin.Close()
	if mkErr != nil {
		t.Fatalf("isolated schema: %v", mkErr)
	}
	if mig != nil {
		if pool != nil {
			_ = pool.Close()
		}
		t.Fatalf("isolated migrate: %v", mig)
	}
	pool.SetMaxOpenConns(4)
	t.Cleanup(func() {
		_ = pool.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		drop, err := sql.Open("pgx", base)
		if err != nil {
			return
		}
		defer func() { _ = drop.Close() }()
		_, _ = drop.ExecContext(dropCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schema))
	})
	return pool
}

// ensureProcessSchema creates + migrates this process's schema once. The shared
// public objects (unaccent extension, immutable_unaccent function) are created
// under a cross-process advisory lock; the per-process schema migration is
// schema-local and needs no lock.
func ensureProcessSchema(base string) {
	procOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		admin, err := sql.Open("pgx", base)
		if err != nil {
			procErr = err
			return
		}
		defer func() { _ = admin.Close() }()
		if err := admin.PingContext(ctx); err != nil {
			procErr = err
			return
		}

		// Serialize the WHOLE per-process setup across processes with one
		// database-global advisory lock. golang-migrate's own lock is keyed by
		// database (not schema), and running many per-schema migrations against
		// the same database concurrently proved unreliable (partially-created
		// schemas). This runs once per process, so the serialization cost is tiny.
		conn, err := admin.Conn(ctx)
		if err != nil {
			procErr = err
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", advisoryKey); err != nil {
			procErr = err
			return
		}
		defer func() { _, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", advisoryKey) }()

		if procErr = createGlobals(ctx, conn); procErr != nil {
			return
		}

		procSchema = fmt.Sprintf("dbtest_p%d", os.Getpid())
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", procSchema)); err != nil {
			procErr = err
			return
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %q", procSchema)); err != nil {
			procErr = err
			return
		}

		pool, err := sql.Open("pgx", withSearchPath(base, procSchema))
		if err != nil {
			procErr = err
			return
		}
		defer func() { _ = pool.Close() }()
		if procErr = appdb.Migrate(pool); procErr != nil {
			return
		}
		truncateStmt, procErr = buildTruncate(ctx, pool, procSchema)
	})
}

// createGlobals creates the shared public objects on an already-advisory-locked
// connection (caller holds the lock).
func createGlobals(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS unaccent WITH SCHEMA public`); err != nil {
		return err
	}
	_, err := conn.ExecContext(ctx, `
		CREATE OR REPLACE FUNCTION public.immutable_unaccent(text) RETURNS text
		LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS
		$$ SELECT public.unaccent('public.unaccent', $1) $$`)
	return err
}

// buildTruncate assembles a single TRUNCATE over every base table in the schema
// (except golang-migrate's bookkeeping), CASCADE so FK order doesn't matter.
func buildTruncate(ctx context.Context, db *sql.DB, schema string) (string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = $1 AND table_type = 'BASE TABLE' AND table_name <> 'schema_migrations'`, schema)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return "", err
		}
		names = append(names, fmt.Sprintf("%q.%q", schema, n))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "SELECT 1", nil
	}
	return "TRUNCATE " + strings.Join(names, ", ") + " RESTART IDENTITY CASCADE", nil
}

// SeedUsers inserts users with the given ids (idempotent) so store tests can
// reference user_id FKs without standing up the auth store.
func SeedUsers(t *testing.T, db *sql.DB, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		if _, err := db.Exec(
			`INSERT INTO users(id, username, password_hash) VALUES($1, $2, '') ON CONFLICT (id) DO NOTHING`,
			id, fmt.Sprintf("testuser%d", id)); err != nil {
			t.Fatalf("seed user %d: %v", id, err)
		}
	}
	// Explicit-id inserts don't advance the IDENTITY sequence, so a later
	// CreateUser (auto id) would collide with a seeded id. Resync past the max.
	if _, err := db.Exec(
		`SELECT setval(pg_get_serial_sequence('users','id'), GREATEST((SELECT MAX(id) FROM users), 1))`); err != nil {
		t.Fatalf("resync users seq: %v", err)
	}
}

// withSearchPath returns the DSN with the connection's search_path pinned to the
// schema (then public, where the extension/helpers live).
func withSearchPath(base, schema string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	return u.String()
}
