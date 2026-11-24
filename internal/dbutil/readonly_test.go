package dbutil

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsReadOnlyErr(t *testing.T) {
	ro := &pgconn.PgError{Code: "25006", Message: "cannot execute UPDATE in a read-only transaction"}
	if !IsReadOnlyErr(ro) {
		t.Error("25006 must be read-only")
	}
	if !IsReadOnlyErr(fmt.Errorf("rotate: %w", ro)) {
		t.Error("wrapped 25006 must be read-only")
	}
	if IsReadOnlyErr(&pgconn.PgError{Code: "23505"}) {
		t.Error("unique violation is not read-only")
	}
	if IsReadOnlyErr(errors.New("cannot execute UPDATE in a read-only transaction")) || IsReadOnlyErr(nil) {
		t.Error("only typed PgError 25006 counts")
	}
}
