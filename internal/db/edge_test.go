package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lgldsilva/jackui/internal/db"
	"github.com/lgldsilva/jackui/internal/dbtest"
)

func TestLatestMigrationVersion(t *testing.T) {
	v, err := db.LatestMigrationVersion()
	if err != nil || v < 4 {
		t.Fatalf("LatestMigrationVersion = %d, %v", v, err)
	}
}

func TestCheckSchema(t *testing.T) {
	pool := dbtest.NewIsolatedDB(t)
	ctx := context.Background()
	latest, _ := db.LatestMigrationVersion()

	set := func(v uint, dirty bool) {
		t.Helper()
		if _, err := pool.ExecContext(ctx, `UPDATE schema_migrations SET version=$1, dirty=$2`, v, dirty); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.CheckSchema(ctx, pool); err != nil {
		t.Fatalf("current schema rejected: %v", err)
	}
	set(latest+1, false)
	if err := db.CheckSchema(ctx, pool); err != nil {
		t.Fatalf("newer schema (primary upgraded first) must be accepted: %v", err)
	}
	set(latest-1, false)
	if err := db.CheckSchema(ctx, pool); err == nil || !strings.Contains(err.Error(), "upgrade the primary first") {
		t.Fatalf("older schema: err = %v", err)
	}
	set(latest, true)
	if err := db.CheckSchema(ctx, pool); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("dirty schema: err = %v", err)
	}
	if _, err := pool.ExecContext(ctx, `DROP TABLE schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckSchema(ctx, pool); err == nil {
		t.Fatal("missing schema_migrations must fail")
	}
}

func TestReadOnlyProbe(t *testing.T) {
	var nilProbe *db.ReadOnlyProbe
	if nilProbe.Degraded() {
		t.Fatal("nil probe (primary) is never degraded")
	}
	pool := dbtest.NewDB(t)
	p := db.NewReadOnlyProbe(pool)
	if p.Check(context.Background()) || p.Degraded() {
		t.Fatal("writable primary reported as degraded")
	}
	_ = pool.Close()
	if !p.Check(context.Background()) || !p.Degraded() {
		t.Fatal("unreachable database must count as degraded")
	}
}
