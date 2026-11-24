package dbutil

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// sqlStateReadOnly is read_only_sql_transaction: a write reached a hot standby.
const sqlStateReadOnly = "25006"

// IsReadOnlyErr reports whether err is a write rejected by a read-only replica.
// An edge node falls back to its local standby when the primary database is
// unreachable, so this is how callers recognise "degraded, try again later".
func IsReadOnlyErr(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == sqlStateReadOnly
}
