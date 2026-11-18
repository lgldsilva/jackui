package watchlist

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A context that is already done must stop every store call before it reaches
// Postgres. Before the *Context migration these methods had no ctx at all.
func TestStoreCallsHonourCancelledContext(t *testing.T) {
	s := newTestStore(t)
	live := context.Background()
	w, err := s.Create(live, 1, Params{Query: "q"})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := map[string]func() error{
		"Create":             func() error { _, err := s.Create(ctx, 1, Params{Query: "x"}); return err },
		"Update":             func() error { return s.Update(ctx, 1, w.ID, Params{Query: "y"}) },
		"Delete":             func() error { return s.Delete(ctx, 1, w.ID) },
		"Get":                func() error { _, err := s.Get(ctx, 1, w.ID); return err },
		"getByID":            func() error { _, err := s.getByID(ctx, w.ID); return err },
		"List":               func() error { _, err := s.List(ctx, 1); return err },
		"ListAll":            func() error { _, err := s.ListAll(ctx); return err },
		"ListDue":            func() error { _, err := s.ListDue(ctx, time.Now()); return err },
		"MarkSeen":           func() error { _, err := s.MarkSeen(ctx, w.ID, "h", "t", "m", 1, 1); return err },
		"MarkAutoDownloaded": func() error { return s.MarkAutoDownloaded(ctx, w.ID, "h") },
		"MarkChecked":        func() error { return s.MarkChecked(ctx, w.ID, time.Now()) },
		"Hits":               func() error { _, err := s.Hits(ctx, 1, w.ID, 10); return err },
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
	if got, err := s.Get(live, 1, w.ID); err != nil || got.Query != "q" {
		t.Fatalf("watchlist after cancelled calls = %+v, %v; want untouched", got, err)
	}
}
