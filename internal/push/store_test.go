package push

import (
	"testing"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2)
	s, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestLoadOrCreateVAPID_StableAcrossCalls(t *testing.T) {
	s := newTestStore(t)
	pub1, priv1, err := s.LoadOrCreateVAPID()
	if err != nil || pub1 == "" || priv1 == "" {
		t.Fatalf("first load: %q %q %v", pub1, priv1, err)
	}
	pub2, priv2, err := s.LoadOrCreateVAPID()
	if err != nil {
		t.Fatal(err)
	}
	if pub2 != pub1 || priv2 != priv1 {
		t.Fatal("VAPID pair must be generated once and reused")
	}
}

func TestSubscribe_UpsertAndUnsubscribe(t *testing.T) {
	s := newTestStore(t)
	if err := s.Subscribe(1, "https://push/ep1", "p", "a"); err != nil {
		t.Fatal(err)
	}
	// Same endpoint re-subscribed by another user → re-owned, not duplicated.
	if err := s.Subscribe(2, "https://push/ep1", "p2", "a2"); err != nil {
		t.Fatal(err)
	}
	subs1, _ := s.SubscriptionsFor(1)
	subs2, _ := s.SubscriptionsFor(2)
	if len(subs1) != 0 || len(subs2) != 1 || subs2[0].P256dh != "p2" {
		t.Fatalf("upsert mismatch: user1=%+v user2=%+v", subs1, subs2)
	}
	if err := s.Unsubscribe(2, "https://push/ep1"); err != nil {
		t.Fatal(err)
	}
	subs2, _ = s.SubscriptionsFor(2)
	if len(subs2) != 0 {
		t.Fatalf("unsubscribe left %+v", subs2)
	}
	// Missing fields rejected.
	if err := s.Subscribe(1, "", "p", "a"); err == nil {
		t.Fatal("expected error for empty endpoint")
	}
}

func TestDeleteEndpoint(t *testing.T) {
	s := newTestStore(t)
	_ = s.Subscribe(1, "https://push/dead", "p", "a")
	if err := s.DeleteEndpoint("https://push/dead"); err != nil {
		t.Fatal(err)
	}
	subs, _ := s.SubscriptionsFor(1)
	if len(subs) != 0 {
		t.Fatalf("endpoint not deleted: %+v", subs)
	}
}

func TestNotificationsFeed(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddNotification(1, "Title A", "body", "magnet:a"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNotification(1, "Title B", "body", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNotification(2, "Other user", "", ""); err != nil {
		t.Fatal(err)
	}
	items, err := s.Notifications(1, 0)
	if err != nil || len(items) != 2 {
		t.Fatalf("Notifications: %v %+v", err, items)
	}
	if items[0].Title != "Title B" {
		t.Fatalf("newest first expected, got %+v", items[0])
	}
	unread, _ := s.UnreadCount(1)
	if unread != 2 {
		t.Fatalf("unread = %d", unread)
	}
	if err := s.MarkAllRead(1); err != nil {
		t.Fatal(err)
	}
	unread, _ = s.UnreadCount(1)
	if unread != 0 {
		t.Fatalf("unread after mark = %d", unread)
	}
	otherUnread, _ := s.UnreadCount(2)
	if otherUnread != 1 {
		t.Fatalf("user 2 unread = %d", otherUnread)
	}
}

func TestNotificationsFeed_TrimsToCap(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < maxFeedPerUser+10; i++ {
		if err := s.AddNotification(1, "t", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.Notifications(1, maxFeedPerUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != maxFeedPerUser {
		t.Fatalf("feed should trim to %d, got %d", maxFeedPerUser, len(items))
	}
}

func TestStore_ErrorPathsOnClosedDB(t *testing.T) {
	// Close the underlying pool (Store.Close is a no-op now that the pool is
	// shared/owned by main) to force the error paths.
	pool := dbtest.NewDB(t)
	s, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if _, _, err := s.LoadOrCreateVAPID(); err == nil {
		t.Fatal("LoadOrCreateVAPID on closed db should error")
	}
	if err := s.AddNotification(1, "t", "", ""); err == nil {
		t.Fatal("AddNotification on closed db should error")
	}
	if _, err := s.Notifications(1, 5); err == nil {
		t.Fatal("Notifications on closed db should error")
	}
	if _, err := s.SubscriptionsFor(1); err == nil {
		t.Fatal("SubscriptionsFor on closed db should error")
	}
	if err := s.MarkAllRead(1); err == nil {
		t.Fatal("MarkAllRead on closed db should error")
	}
}
