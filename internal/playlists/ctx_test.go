package playlists

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
	p, err := s.Create(live, 1, "pl", "")
	if err != nil {
		t.Fatal(err)
	}
	it, err := s.AddItem(live, p.ID, 1, Item{Title: "t", Magnet: "magnet:?xt=urn:btih:aaaa", InfoHash: "aaaa"}, false)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := map[string]func() error{
		"Create":     func() error { _, err := s.Create(ctx, 1, "x", ""); return err },
		"List":       func() error { _, err := s.List(ctx, 1, false); return err },
		"Get":        func() error { _, err := s.Get(ctx, p.ID, 1, false); return err },
		"Update":     func() error { return s.Update(ctx, p.ID, 1, "n", "d", false) },
		"Delete":     func() error { return s.Delete(ctx, p.ID, 1, false) },
		"AddItem":    func() error { _, err := s.AddItem(ctx, p.ID, 1, Item{Title: "t", Magnet: "m"}, false); return err },
		"Items":      func() error { _, err := s.Items(ctx, p.ID, 1, false); return err },
		"RemoveItem": func() error { return s.RemoveItem(ctx, p.ID, it.ID, 1, false) },
		"Reorder":    func() error { return s.Reorder(ctx, p.ID, it.ID, 1, 0, false) },
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
	// Nothing above may have been applied.
	if got, err := s.Items(live, p.ID, 1, false); err != nil || len(got) != 1 {
		t.Fatalf("items after cancelled calls = %v, %v; want the single original item", got, err)
	}
}
