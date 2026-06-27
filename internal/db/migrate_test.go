package db_test

import (
	"context"
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
func TestOpenAndMigrate(t *testing.T) {
	dsn := os.Getenv(envURL)
	if dsn == "" {
		t.Skipf("%s not set; skipping (needs a PostgreSQL test database)", envURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := appdb.Open(ctx, dsn, 15*time.Second)
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
