package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestServerDoesNotLinkSQLite guards against a SQLite driver creeping back into
// the server binary. All durable state lives in PostgreSQL; the legacy one-shot
// auth.db import lives in cmd/migrate-auth so the server never links it.
//
// CGO_ENABLED=0 mirrors the release images (Alpine build stage, no C compiler):
// with cgo on, anacrolix/torrent/storage pulls its own optional cgo SQLite
// piece-completion backend, which is outside this guard's scope.
func TestServerDoesNotLinkSQLite(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	cmd := exec.Command(goBin, "list", "-e", "-deps", ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if strings.Contains(strings.ToLower(pkg), "sqlite") {
			t.Errorf("cmd/server depends on %s; keep SQLite out of the server binary (use cmd/migrate-auth)", pkg)
		}
	}
}

func TestRemovedSubcommand(t *testing.T) {
	err := removedSubcommand([]string{"migrate-auth", "--from", "x"})
	if err == nil || !strings.Contains(err.Error(), "cmd/migrate-auth") {
		t.Fatalf("migrate-auth: got %v, want a pointer to cmd/migrate-auth", err)
	}
	for _, args := range [][]string{nil, {"/app/config.yaml"}, {"healthcheck"}} {
		if err := removedSubcommand(args); err != nil {
			t.Errorf("removedSubcommand(%q) = %v, want nil", args, err)
		}
	}
}
