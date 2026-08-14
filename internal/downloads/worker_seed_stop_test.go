package downloads

import (
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts
}

// TestShouldAutoSeedSkipsStoppedRows verifies the filtering logic used by
// autoSeedCompleted: completed rows with SeedStoppedAt set are NOT candidates,
// regardless of tracker matching.
func TestShouldAutoSeedSkipsStoppedRows(t *testing.T) {
	stoppedAt := mustTime(t, "2026-08-14T00:00:00Z")
	cases := []struct {
		name    string
		status  string
		hash    string
		stopped bool
		match   bool
		want    bool
	}{
		{"completed matching", StatusCompleted, "aabbccddeeff00112233445566778899aabbccdd", false, true, true},
		{"completed stopped", StatusCompleted, "aabbccddeeff00112233445566778899aabbccdd", true, true, false},
		{"completed no match", StatusCompleted, "aabbccddeeff00112233445566778899aabbccdd", false, false, false},
		{"paused matching", StatusPaused, "aabbccddeeff00112233445566778899aabbccdd", false, true, false},
		{"completed empty hash", StatusCompleted, "", false, true, false},
		{"completed bad hash", StatusCompleted, "nothex", false, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Download{Status: tc.status, InfoHash: tc.hash}
			if tc.stopped {
				d.SeedStoppedAt = &stoppedAt
			}
			got := shouldAutoSeed(d, func(metainfo.Hash) bool { return tc.match })
			if got != tc.want {
				t.Fatalf("shouldAutoSeed = %v, want %v", got, tc.want)
			}
		})
	}
}
