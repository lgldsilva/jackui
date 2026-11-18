package tmdb

import (
	"context"
	"testing"
)

func goneCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// Reads honour the caller's context: a done ctx behaves like a miss.
func TestCacheAndSnapshotReadsHonourCancelledContext(t *testing.T) {
	c := newSnapTestClient(t)
	live := context.Background()
	c.setCached(live, "k", &Match{Title: "T"})
	if _, ok := c.getCached(live, "k"); !ok {
		t.Fatal("setup: cache miss")
	}
	if _, ok := c.getCached(goneCtx(), "k"); ok {
		t.Error("getCached on a cancelled ctx must miss")
	}
	c.saveSnapshot(live, "2026-W01", []Match{{TmdbID: 7}})
	if got := c.prevWeekRanks(goneCtx(), "2026-W02"); len(got) != 0 {
		t.Errorf("prevWeekRanks on a cancelled ctx = %v, want empty", got)
	}
	if got := c.prevWeekRanks(live, "2026-W02"); got[7] != 0 || len(got) != 1 {
		t.Errorf("prevWeekRanks = %v", got)
	}
}

// The writes follow a paid-for TMDB round-trip, so a vanished caller must not
// lose them.
func TestCacheAndSnapshotWritesSurviveCancelledContext(t *testing.T) {
	c := newSnapTestClient(t)
	c.setCached(goneCtx(), "gone", &Match{Title: "G"})
	if m, ok := c.getCached(context.Background(), "gone"); !ok || m == nil || m.Title != "G" {
		t.Fatalf("cache write lost on a cancelled ctx: %v %v", m, ok)
	}
	c.saveSnapshot(goneCtx(), "2026-W01", []Match{{TmdbID: 9}, {TmdbID: 8}})
	got := c.prevWeekRanks(context.Background(), "2026-W02")
	if got[9] != 0 || got[8] != 1 {
		t.Fatalf("snapshot lost on a cancelled ctx: %v", got)
	}
}
