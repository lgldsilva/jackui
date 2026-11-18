package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunExitCodes covers the CLI wrapper without a database: argument and
// source-file errors must exit non-zero with a prefixed message on stderr.
func TestRunExitCodes(t *testing.T) {
	t.Setenv("JACKUI_DATABASE_URL", "")
	missing := filepath.Join(t.TempDir(), "absent.db")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"bad flag", []string{"--nope"}, "flag provided but not defined"},
		{"no dsn", []string{"--from", missing}, "missing --to DSN"},
		{"no source", []string{"--from", missing, "--to", "postgres://x"}, "source auth.db"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := run(tc.args, &stderr); code != 1 {
				t.Fatalf("exit code = %d, want 1", code)
			}
			if got := stderr.String(); !strings.HasPrefix(got, "migrate-auth: ") || !strings.Contains(got, tc.want) {
				t.Errorf("stderr = %q, want prefix and %q", got, tc.want)
			}
		})
	}
}
