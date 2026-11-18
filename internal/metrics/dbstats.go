package metrics

import (
	"database/sql"
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// RegisterDBStats exports the shared pool's sql.DBStats (open / in-use / idle
// connections, wait count and wait duration) as go_sql_*{db_name="jackui"}.
// The pool is capped (JACKUI_PG_MAX_CONNS, default 20); a growing wait count
// is the signal that requests are queueing for a connection. Registering twice
// (tests, re-init) is a no-op.
func RegisterDBStats(db *sql.DB) error {
	if db == nil {
		return nil
	}
	err := prometheus.Register(collectors.NewDBStatsCollector(db, "jackui"))
	var are prometheus.AlreadyRegisteredError
	if errors.As(err, &are) {
		return nil
	}
	return err
}
