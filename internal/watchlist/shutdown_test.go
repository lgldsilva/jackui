package watchlist

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/jackett"
)

// hangingSearcher models a Jackett that never answers: the context-aware entry
// point returns only when cancelled, the legacy one only when released.
type hangingSearcher struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func newHangingSearcher() *hangingSearcher {
	return &hangingSearcher{started: make(chan struct{}, 16), release: make(chan struct{})}
}

func (h *hangingSearcher) enter() {
	h.mu.Lock()
	h.calls++
	h.mu.Unlock()
	h.started <- struct{}{}
}

func (h *hangingSearcher) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

func (h *hangingSearcher) Search(query, category string, indexers []string) ([]jackett.Result, error) {
	h.enter()
	<-h.release
	return nil, nil
}

func (h *hangingSearcher) SearchContext(ctx context.Context, query, category string, indexers []string) ([]jackett.Result, error) {
	h.enter()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.release:
		return nil, nil
	}
}

// Stop used to wait for the whole pass: every due list, each behind a Jackett
// call that can take minutes. It must now abort the in-flight search and skip
// the remaining lists, leaving them due for the next boot.
func TestStopAbortsInFlightPass(t *testing.T) {
	s := newTestStore(t)
	for _, q := range []string{"one", "two", "three"} {
		if _, err := s.Create(1, params(q, "", 0, "")); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	if _, err := s.db.Exec(`UPDATE watchlists SET next_check_at=NULL`); err != nil {
		t.Fatalf("making lists due: %v", err)
	}
	srch := newHangingSearcher()
	t.Cleanup(func() { close(srch.release) })
	w := NewWorker(s, srch, &safeNotifier{}, "", time.Hour)
	w.startDelay = 0
	w.Start()

	select {
	case <-srch.started:
	case <-time.After(10 * time.Second):
		t.Fatal("worker never started a search")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Stop()
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return while a search was in flight")
	}
	if n := srch.count(); n != 1 {
		t.Errorf("searches = %d, want 1 (remaining lists must be skipped)", n)
	}
	due, err := s.ListDue(time.Now())
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(due) != 2 {
		t.Errorf("lists still due = %d, want 2 (skipped lists must not be re-armed)", len(due))
	}
}

// A pass that starts after Stop must not search or re-arm anything.
func TestProcessListsSkipsEverythingOnceStopped(t *testing.T) {
	s := newTestStore(t)
	wl, err := s.Create(1, params("q", "", 0, ""))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	srch := &countingSearcher{}
	w := NewWorker(s, srch, &safeNotifier{}, "", time.Hour)
	w.Stop()

	w.processLists([]Watchlist{*wl})

	if got := srch.seen(); len(got) != 0 {
		t.Errorf("searches after Stop = %v, want none", got)
	}
	got, err := s.Get(1, wl.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.LastChecked.IsZero() {
		t.Error("a skipped list must not be marked checked")
	}
}

// Searchers without a context-aware entry point keep working unchanged.
func TestSearchFallsBackToPlainSearcher(t *testing.T) {
	srch := &countingSearcher{results: []jackett.Result{{Title: "r"}}}
	res, err := search(context.Background(), srch, "plain", "")
	if err != nil || len(res) != 1 {
		t.Fatalf("search = %v, %v", res, err)
	}
	if got := srch.seen(); len(got) != 1 || got[0] != "plain" {
		t.Errorf("queries = %v, want [plain]", got)
	}
}
