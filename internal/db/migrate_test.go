package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	appdb "github.com/lgldsilva/jackui/internal/db"
)

const envURL = "JACKUI_TEST_DATABASE_URL"

// TestOpenAndMigrate proves the foundation works against a real Postgres: the
// pgx pool dials, the golang-migrate runner (postgres driver over the pgx-backed
// *sql.DB) applies the schema, the unaccent extension lands, and a second Up is
// a no-op (idempotent). Skips when no test database is configured.
//
// Runs inside a private schema (never public) so it can't pollute other tests
// that share this database via the public fallback in their search_path.
func TestOpenAndMigrate(t *testing.T) {
	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("%s not set; skipping (needs a PostgreSQL test database)", envURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	defer func() { _ = admin.Close() }()
	schema := fmt.Sprintf("openmigrate_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %q", schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)) })

	u, _ := url.Parse(base)
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()

	pool, err := appdb.Open(ctx, u.String(), 15*time.Second)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = pool.Close() }()

	if err := appdb.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Idempotent re-run.
	if err := appdb.Migrate(pool); err != nil {
		t.Fatalf("migrate (second run): %v", err)
	}

	var present bool
	if err := pool.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'unaccent')").
		Scan(&present); err != nil {
		t.Fatalf("check unaccent: %v", err)
	}
	if !present {
		t.Fatal("unaccent extension not installed by migration")
	}
}
