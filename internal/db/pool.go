// Package db owns the shared PostgreSQL connection pool and schema migrations.
// All stores receive the single *sql.DB created here instead of opening their
// own SQLite files.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver registered as "pgx"
)

// Open dials the Postgres pool and waits for it to accept connections. The
// pool may not be reachable at boot (the sidecar is still starting, or the
// gluetun netns was just recreated on port rotation), so it pings with
// exponential backoff up to maxWait instead of failing immediately.
//
// sql.Open never actually connects; the loop below is what proves the DB is up.
func Open(ctx context.Context, dsn string, maxWait time.Duration) (*sql.DB, error) {
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	// Conservative pool sizing for a homeserver Postgres. Tunable later if a
	// hot store needs more; the point now is to drop the SQLite single-writer
	// (MaxOpenConns(1)) bottleneck.
	pool.SetMaxOpenConns(20)
	pool.SetMaxIdleConns(10)
	pool.SetConnMaxLifetime(time.Hour)

	deadline := time.Now().Add(maxWait)
	backoff := time.Second
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = pool.PingContext(pingCtx)
		cancel()
		if err == nil {
			return pool, nil
		}
		if time.Now().After(deadline) {
			_ = pool.Close()
			return nil, fmt.Errorf("postgres unreachable after %s: %w", maxWait, err)
		}
		select {
		case <-ctx.Done():
			_ = pool.Close()
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 16*time.Second {
			backoff *= 2
		}
	}
}
