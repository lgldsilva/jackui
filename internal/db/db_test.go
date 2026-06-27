package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/db"
	"github.com/lgldsilva/jackui/internal/dbtest"
)

// Smoke test: dbtest.NewDB must hand back a pool whose private schema already
// ran the migrations (the unaccent extension is reachable), proving Open +
// Migrate + schema isolation work against a real Postgres.
func TestMigrateAndExtension(t *testing.T) {
	pool := dbtest.NewDB(t)

	var got string
	if err := pool.QueryRow(`SELECT unaccent('Pelé')`).Scan(&got); err != nil {
		t.Fatalf("unaccent: %v", err)
	}
	if got != "Pele" {
		t.Errorf("unaccent('Pelé') = %q, want %q", got, "Pele")
	}
}

// Open should surface an error (not hang) when the DSN points nowhere, within
// the backoff budget.
func TestOpenUnreachable(t *testing.T) {
	ctx := context.Background()
	_, err := db.Open(ctx, "postgres://nobody:nobody@127.0.0.1:1/none?sslmode=disable", 2*time.Second)
	if err == nil {
		t.Fatal("expected error for unreachable DSN")
	}
}
