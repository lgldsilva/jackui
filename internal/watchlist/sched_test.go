package watchlist

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/jackett"
)

// countingSearcher is a goroutine-safe Searcher that records the queries it saw.
type countingSearcher struct {
	mu      sync.Mutex
	queries []string
	results []jackett.Result
}

func (c *countingSearcher) Search(query, category string, indexers []string) ([]jackett.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, query)
	return c.results, nil
}

func (c *countingSearcher) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.queries...)
}

// safeNotifier is a goroutine-safe notification recorder.
type safeNotifier struct {
	mu     sync.Mutex
	titles []string
	// notified, when non-nil, receives a signal per Notify call so tests can
	// await a notification deterministically instead of polling.
	notified chan struct{}
}

func (n *safeNotifier) Notify(ctx context.Context, topic, title, body, magnet string) error {
	n.mu.Lock()
	n.titles = append(n.titles, title)
	ch := n.notified
	n.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return nil
}

func (n *safeNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.titles)
}

func TestCreatePersistsScheduleAndArmsNextCheck(t *testing.T) {
	s := newTestStore(t)
	w, err := s.Create(1, Params{Query: "q", MinSeeders: 1, Schedule: Schedule{Kind: SchedDaily, Hour: 12, Minute: 30}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.Kind != SchedDaily || w.Hour != 12 || w.Minute != 30 {
		t.Fatalf("schedule not persisted: %+v", w.Schedule)
	}
	now := time.Now()
	if w.NextCheckAt.IsZero() || !w.NextCheckAt.After(now.Add(-2*time.Second)) || w.NextCheckAt.After(now.Add(24*time.Hour)) {
		t.Fatalf("NextCheckAt out of range: %v", w.NextCheckAt)
	}
}

func TestUpdateReschedules(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, Params{Query: "q", MinSeeders: 1, Schedule: Schedule{Kind: SchedInterval, Minutes: 5}})
	if err := s.Update(1, w.ID, Params{Query: "q", MinSeeders: 1, Schedule: Schedule{Kind: SchedWeekly, Weekday: 6, Hour: 8, Minute: 0}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.Get(1, w.ID)
	if got.Kind != SchedWeekly || got.Weekday != 6 || got.Hour != 8 {
		t.Fatalf("schedule not updated: %+v", got.Schedule)
	}
	// weekly is at least tomorrow-ish or later today, but definitely further out
	// than the old 5-minute interval
	if !got.NextCheckAt.After(time.Now().Add(5 * time.Minute)) {
		t.Fatalf("NextCheckAt not recomputed: %v", got.NextCheckAt)
	}
}

func TestListDue(t *testing.T) {
	s := newTestStore(t)
	due, _ := s.Create(1, Params{Query: "due", MinSeeders: 1})
	notDue, _ := s.Create(1, Params{Query: "not-due", MinSeeders: 1})
	if err := s.MarkChecked(due.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("MarkChecked: %v", err)
	}
	if err := s.MarkChecked(notDue.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("MarkChecked: %v", err)
	}
	lists, err := s.ListDue(time.Now())
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(lists) != 1 || lists[0].ID != due.ID {
		t.Fatalf("ListDue = %+v, want only id=%d", lists, due.ID)
	}
}

func TestListDue_NullNextCheckIsDue(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, Params{Query: "legacy", MinSeeders: 1})
	if _, err := s.db.Exec(`UPDATE watchlists SET next_check_at=NULL WHERE id=?`, w.ID); err != nil {
		t.Fatalf("forcing NULL: %v", err)
	}
	lists, err := s.ListDue(time.Now())
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(lists) != 1 {
		t.Fatalf("expected NULL next_check_at to be due, got %+v", lists)
	}
}

func TestWorker_RunDueOnlyProcessesDueItems(t *testing.T) {
	s := newTestStore(t)
	due, _ := s.Create(1, Params{Query: "due-query", MinSeeders: 1, Schedule: Schedule{Kind: SchedInterval, Minutes: 5}})
	notDue, _ := s.Create(1, Params{Query: "future-query", MinSeeders: 1, Schedule: Schedule{Kind: SchedDaily, Hour: 23, Minute: 59}})
	_ = s.MarkChecked(due.ID, time.Now().Add(-time.Minute))
	_ = s.MarkChecked(notDue.ID, time.Now().Add(time.Hour))

	searcher := &countingSearcher{results: []jackett.Result{{InfoHash: "abc", Title: "Hit", MagnetURI: "m:1", Seeders: 9}}}
	notifier := &safeNotifier{}
	w := NewWorker(s, searcher, notifier, "topic", 15*time.Minute)
	w.runDue()

	if got := searcher.seen(); len(got) != 1 || got[0] != "due-query" {
		t.Fatalf("searched %v, want only due-query", got)
	}
	// the due item must be re-armed into the future
	after, _ := s.Get(1, due.ID)
	if !after.NextCheckAt.After(time.Now()) {
		t.Fatalf("due item not re-armed: %v", after.NextCheckAt)
	}
}

func TestWorker_KickChecksOneItemImmediately(t *testing.T) {
	s := newTestStore(t)
	// schedule far in the future — only the kick can trigger the check
	w, _ := s.Create(1, Params{Query: "kicked", MinSeeders: 1, Schedule: Schedule{Kind: SchedDaily, Hour: 23, Minute: 59}})
	_ = s.MarkChecked(w.ID, time.Now().Add(time.Hour))

	searcher := &countingSearcher{results: []jackett.Result{{InfoHash: "abc", Title: "Hit", MagnetURI: "m:1", Seeders: 9}}}
	notifier := &safeNotifier{notified: make(chan struct{}, 1)}
	wk := NewWorker(s, searcher, notifier, "topic", 15*time.Minute)
	wk.startDelay = time.Millisecond
	wk.tick = time.Hour // make sure the scheduled pass never fires during the test
	wk.Start()
	defer wk.Stop()

	wk.Kick(w.ID)
	select {
	case <-notifier.notified: // the kick's async check ran and notified
	case <-time.After(5 * time.Second):
		t.Fatal("no notification after kick within 5s")
	}
	if notifier.count() != 1 {
		t.Fatalf("expected 1 notification after kick, got %d", notifier.count())
	}
}

func TestWorker_KickIsNilSafeAndTolerant(t *testing.T) {
	var nilWorker *Worker
	nilWorker.Kick(1) // must not panic

	s := newTestStore(t)
	w := NewWorker(s, &countingSearcher{}, &safeNotifier{}, "topic", time.Minute)
	w.checkOne(999) // unknown id — logged, no panic
	for i := 0; i < 100; i++ {
		w.Kick(i) // overflow the buffer — must never block
	}
}

func TestNewWorker_SeedsStoreDefaultEvery(t *testing.T) {
	s := newTestStore(t)
	NewWorker(s, &countingSearcher{}, &safeNotifier{}, "", 42*time.Minute)
	if s.DefaultEvery != 42*time.Minute {
		t.Fatalf("DefaultEvery = %v, want 42m", s.DefaultEvery)
	}
	// "server default" items must inherit it
	w, _ := s.Create(1, Params{Query: "q", MinSeeders: 1})
	want := time.Now().Add(42 * time.Minute)
	if w.NextCheckAt.Before(want.Add(-2*time.Second)) || w.NextCheckAt.After(want.Add(2*time.Second)) {
		t.Fatalf("NextCheckAt = %v, want ≈ %v", w.NextCheckAt, want)
	}
}

func TestMarkCheckedRoundTripsNextCheckAt(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, Params{Query: "q", MinSeeders: 1})
	next := time.Now().Add(30 * time.Minute)
	if err := s.MarkChecked(w.ID, next); err != nil {
		t.Fatalf("MarkChecked: %v", err)
	}
	got, err := s.Get(1, w.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// stored truncated to the second — allow 2s of slack
	if d := got.NextCheckAt.Sub(next); d < -2*time.Second || d > 2*time.Second {
		t.Fatalf("NextCheckAt = %v, want ≈ %v", got.NextCheckAt, next)
	}
}

func TestWorker_RunDueListError(t *testing.T) {
	pool := dbtest.NewDB(t)
	s, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorker(s, &countingSearcher{}, &safeNotifier{}, "topic", time.Minute)
	pool.Close()
	w.runDue() // must not panic when the store is closed
}
