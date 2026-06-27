// Package dbtest provides an isolated PostgreSQL handle for store tests.
//
// Acquisition strategy (hybrid — see the migration plan):
//  1. JACKUI_TEST_DATABASE_URL set → connect to that Postgres, give the test
//     its own freshly-migrated schema, drop it on cleanup. This is the CI path
//     (a Postgres service is attached to the test stage).
//  2. otherwise → t.Skip with a clear message. (A future embedded-postgres
//     fallback can slot in here for offline dev.)
//
// Each test gets a private schema so tests stay independent and can run in
// parallel against one shared server.
package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	appdb "github.com/lgldsilva/jackui/internal/db"
)

const envURL = "JACKUI_TEST_DATABASE_URL"

var schemaSeq atomic.Int64

// NewDB returns a *sql.DB scoped to a private, freshly-migrated schema. The
// schema is dropped and the pool closed via t.Cleanup. Skips the test when no
// test database is configured.
func NewDB(t *testing.T) *sql.DB {
	t.Helper()
	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("%s not set; skipping (needs a PostgreSQL test database)", envURL)
	}

	schema := uniqueSchema(t)

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer func() { _ = admin.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		t.Fatalf("ping test postgres: %v", err)
	}
	if _, err := admin.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %q", schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	dsn, err := withSearchPath(base, schema)
	if err != nil {
		t.Fatalf("build dsn: %v", err)
	}
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	if err := appdb.Migrate(pool); err != nil {
		_ = pool.Close()
		t.Fatalf("migrate test schema: %v", err)
	}

	t.Cleanup(func() {
		_ = pool.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		drop, err := sql.Open("pgx", base)
		if err != nil {
			return
		}
		defer func() { _ = drop.Close() }()
		_, _ = drop.ExecContext(dropCtx, fmt.Sprintf("DROP SCHEMA %q CASCADE", schema))
	})

	return pool
}

func uniqueSchema(t *testing.T) string {
	name := strings.ToLower(t.Name())
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	trimmed := b.String()
	if len(trimmed) > 40 {
		trimmed = trimmed[:40]
	}
	return fmt.Sprintf("test_%s_%d", trimmed, schemaSeq.Add(1))
}

// withSearchPath returns the DSN with the connection's search_path pinned to
// the given schema, so migrations and queries land there (current_schema()).
func withSearchPath(base, schema string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	// Tables land in the private schema (first), extensions/helpers resolve from
	// public (second) — mirrors production where everything lives in public.
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	return u.String(), nil
}
