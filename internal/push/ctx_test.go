package push

import (
	"context"
	"errors"
	"testing"
)

// A done context must stop every store call before it reaches Postgres.
func TestStoreCallsHonourCancelledContext(t *testing.T) {
	s := newTestStore(t)
	live := context.Background()
	if err := s.Subscribe(live, 1, "https://push.example/e1", "p", "a"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := map[string]func() error{
		"LoadOrCreateVAPID": func() error { _, _, err := s.LoadOrCreateVAPID(ctx); return err },
		"Subscribe":         func() error { return s.Subscribe(ctx, 1, "https://push.example/e2", "p", "a") },
		"Unsubscribe":       func() error { return s.Unsubscribe(ctx, 1, "https://push.example/e1") },
		"DeleteEndpoint":    func() error { return s.DeleteEndpoint(ctx, "https://push.example/e1") },
		"SubscriptionsFor":  func() error { _, err := s.SubscriptionsFor(ctx, 1); return err },
		"AddNotification":   func() error { return s.AddNotification(ctx, 1, "t", "b", "") },
		"Notifications":     func() error { _, err := s.Notifications(ctx, 1, 10); return err },
		"UnreadCount":       func() error { _, err := s.UnreadCount(ctx, 1); return err },
		"MarkAllRead":       func() error { return s.MarkAllRead(ctx, 1) },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want context.Canceled", name, err)
		}
	}
	subs, err := s.SubscriptionsFor(live, 1)
	if err != nil || len(subs) != 1 {
		t.Fatalf("subscriptions after cancelled calls = %d, %v; want untouched", len(subs), err)
	}
}
