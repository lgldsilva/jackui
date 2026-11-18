package history

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/jackett"
)

// A context that is already done must stop every store call before it reaches
// Postgres. Before the *Context migration these methods had no ctx at all, so
// request cancellation never got past the handler.
func TestStoreCallsHonourCancelledContext(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := []jackett.Result{{Title: "x", InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	calls := map[string]func() error{
		"Save":          func() error { return s.Save(ctx, "q", res, 1, false) },
		"RecentEntries": func() error { _, err := s.RecentEntries(ctx, 10, 1, false); return err },
		"Search":        func() error { _, err := s.Search(ctx, "q", 1, false); return err },
		"SearchAll":     func() error { _, err := s.SearchAll(ctx, "q", 10, 1, false); return err },
		"GetResult":     func() error { _, err := s.GetResult(ctx, 1, 1, false); return err },
		"DeleteAll":     func() error { return s.DeleteAll(ctx, 1, false) },
		"DeleteQuery":   func() error { return s.DeleteQuery(ctx, "q", 1, false) },
		"Count":         func() error { _, err := s.DistinctQueryCount(ctx, 1); return err },
	}
	for name, call := range calls {
		start := time.Now()
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want context.Canceled", name, err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s took %v on a cancelled ctx", name, d)
		}
	}
}

// A slow statement under a short deadline is interrupted server-side, not
// run to completion.
func TestStoreSlowQueryHitsDeadline(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := s.db.ExecContext(ctx, `SELECT pg_sleep(5)`)
	if err == nil {
		t.Fatal("pg_sleep(5) finished under a 150ms deadline")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("deadline not enforced: took %v", d)
	}
}
