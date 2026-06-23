package dbutil

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// The pragma constants must compose into a DSN that SQLite actually honours —
// every store appends PragmaBusy5s to avoid intermittent SQLITE_BUSY under
// concurrent access. This guards the constant's value (5000ms) and that the
// driver applies it.
func TestPragmasComposeIntoEffectiveDSN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	db, err := sql.Open(DriverName, path+PragmaWAL+PragmaFK+PragmaBusy5s)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	var busy int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busy != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busy)
	}

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}

	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}
