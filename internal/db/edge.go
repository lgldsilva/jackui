package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// LatestMigrationVersion is the highest migration number embedded in this binary.
func LatestMigrationVersion() (uint, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return 0, err
	}
	var latest uint
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			continue
		}
		v, perr := strconv.ParseUint(num, 10, 32)
		if perr == nil && uint(v) > latest {
			latest = uint(v)
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf("no embedded migrations found")
	}
	return latest, nil
}

// CheckSchema is the edge's replacement for Migrate: an edge never runs DDL (its
// pool may be pointing at a read-only standby, and the primary owns the schema),
// it only refuses to start on a schema older than its code or mid-migration. A
// NEWER schema is accepted with a warning — migrations are additive and the
// supported rollout is primary first, edges right after.
func CheckSchema(ctx context.Context, pool *sql.DB) error {
	want, err := LatestMigrationVersion()
	if err != nil {
		return err
	}
	var have uint
	var dirty bool
	if err := pool.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&have, &dirty); err != nil {
		return fmt.Errorf("read schema version (has the primary migrated this database?): %w", err)
	}
	switch {
	case dirty:
		return fmt.Errorf("schema version %d is dirty (a migration failed on the primary)", have)
	case have < want:
		return fmt.Errorf("schema version %d is older than this binary's %d: upgrade the primary first", have, want)
	case have > want:
		log.Printf("WARNING: schema version %d is newer than this binary's %d — upgrade this edge to the primary's version", have, want)
	}
	return nil
}

// ReadOnlyProbe tracks whether the pool is currently served by a read-only
// standby. With a multi-host DSN (primary first, local replica second) new
// connections fall back to the replica when the primary is down; the probe is
// how the edge notices and switches to degraded mode.
type ReadOnlyProbe struct {
	pool     *sql.DB
	degraded atomic.Bool
}

func NewReadOnlyProbe(pool *sql.DB) *ReadOnlyProbe { return &ReadOnlyProbe{pool: pool} }

// Degraded reports the last observed state. Safe on a nil probe (primary nodes).
func (p *ReadOnlyProbe) Degraded() bool { return p != nil && p.degraded.Load() }

// Check runs one probe and updates the state. An unreachable database counts as
// degraded too: writes would fail either way.
func (p *ReadOnlyProbe) Check(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var inRecovery bool
	err := p.pool.QueryRowContext(ctx, `SELECT pg_is_in_recovery()`).Scan(&inRecovery)
	now := err != nil || inRecovery
	if p.degraded.Swap(now) != now {
		log.Printf("database degraded=%v (err=%v)", now, err)
	}
	return now
}

// Run probes every interval until ctx is cancelled.
func (p *ReadOnlyProbe) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	p.Check(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.Check(ctx)
		}
	}
}
