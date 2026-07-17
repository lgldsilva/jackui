package history

import (
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/jackett"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	s, err := New(pool)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestSaveAndSearch(t *testing.T) {
	s := newTestStore(t)

	results := []jackett.Result{
		{Title: "Ubuntu 22.04", Tracker: "TPB", Seeders: 100, InfoHash: "abc123", MagnetURI: "magnet:?xt=urn:btih:abc123"},
		{Title: "Ubuntu 20.04", Tracker: "RARBG", Seeders: 50, InfoHash: "def456", MagnetURI: "magnet:?xt=urn:btih:def456"},
	}

	if err := s.Save("ubuntu", results, 0, false); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cached, err := s.Search("ubuntu", 0, true)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(cached) != 2 {
		t.Fatalf("expected 2 results, got %d", len(cached))
	}

	for _, r := range cached {
		if !r.Cached {
			t.Errorf("result %q: Cached should be true", r.Title)
		}
	}
}

func TestSaveDedupByInfoHash(t *testing.T) {
	s := newTestStore(t)

	r := jackett.Result{Title: "Ubuntu 22.04", InfoHash: "abc123", MagnetURI: "magnet:?xt=urn:btih:abc123"}

	s.Save("ubuntu", []jackett.Result{r}, 0, false)
	s.Save("ubuntu", []jackett.Result{r}, 0, false) // duplicate

	cached, _ := s.Search("ubuntu", 0, true)
	if len(cached) != 1 {
		t.Fatalf("expected 1 (deduped), got %d", len(cached))
	}
}

func TestSaveEmptyInfoHash(t *testing.T) {
	s := newTestStore(t)

	r1 := jackett.Result{Title: "Ubuntu 22.04", Tracker: "A", InfoHash: ""}
	r2 := jackett.Result{Title: "Ubuntu 22.04", Tracker: "B", InfoHash: ""}

	s.Save("ubuntu", []jackett.Result{r1, r2}, 0, false)

	cached, _ := s.Search("ubuntu", 0, true)
	if len(cached) != 2 {
		t.Fatalf("expected 2 (no dedup without hash), got %d", len(cached))
	}
}

func TestSearchCaseInsensitive(t *testing.T) {
	s := newTestStore(t)

	s.Save("Ubuntu", []jackett.Result{
		{Title: "Ubuntu 22.04", InfoHash: "abc"},
	}, 0, false)

	cached, _ := s.Search("ubuntu", 0, true)
	if len(cached) != 1 {
		t.Fatalf("expected 1 result for case-insensitive match, got %d", len(cached))
	}
}

// Multi-user isolation: results saved by user A are not visible to user B (unless admin).
func TestSearchPerUserIsolation(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{{Title: "Ubuntu for A", InfoHash: "a1"}}, 1, false)
	s.Save("ubuntu", []jackett.Result{{Title: "Ubuntu for B", InfoHash: "b1"}}, 2, false)

	// User A sees only their result
	rA, _ := s.Search("ubuntu", 1, false)
	if len(rA) != 1 || rA[0].Title != "Ubuntu for A" {
		t.Fatalf("user A: expected only their result, got %v", rA)
	}

	// User B sees only their result
	rB, _ := s.Search("ubuntu", 2, false)
	if len(rB) != 1 || rB[0].Title != "Ubuntu for B" {
		t.Fatalf("user B: expected only their result, got %v", rB)
	}

	// Admin (includeAll=true) sees both
	rAll, _ := s.Search("ubuntu", 0, true)
	if len(rAll) != 2 {
		t.Fatalf("admin: expected both results, got %d", len(rAll))
	}
}

func TestRecentEntries(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{{Title: "Ubuntu", InfoHash: "a1"}}, 0, false)
	s.Save("debian", []jackett.Result{{Title: "Debian", InfoHash: "b2"}}, 0, false)
	s.Save("fedora", []jackett.Result{{Title: "Fedora", InfoHash: "c3"}}, 0, false)

	entries, err := s.RecentEntries(10, 0, true)
	if err != nil {
		t.Fatalf("RecentEntries: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
}

func TestSearchAll(t *testing.T) {
	s := newTestStore(t)

	s.Save("breaking bad", []jackett.Result{
		{Title: "Breaking.Bad.S01E01.1080p.BluRay", InfoHash: "bb1"},
		{Title: "Breaking Bad S02 Complete 720p", InfoHash: "bb2"},
	}, 0, false)
	s.Save("better call saul", []jackett.Result{
		{Title: "Better.Call.Saul.S04.1080p", InfoHash: "bcs1"},
	}, 0, false)

	results, err := s.SearchAll("1080p", 100, 0, true)
	if err != nil {
		t.Fatalf("SearchAll: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results matching '1080p', got %d", len(results))
	}

	results, err = s.SearchAll("break", 100, 0, true)
	if err != nil {
		t.Fatalf("SearchAll prefix: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for 'break' prefix, got %d", len(results))
	}

	for _, r := range results {
		if r.Query == "" {
			t.Fatalf("expected Query field populated, got empty")
		}
		if !r.Cached {
			t.Fatalf("expected Cached=true")
		}
	}
}

func TestCleanup(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{{Title: "Old Ubuntu", InfoHash: "old1"}}, 0, false)

	s.db.Exec("UPDATE results SET saved_at = ? WHERE info_hash = 'old1'",
		time.Now().Add(-100*24*time.Hour).Format("2006-01-02 15:04:05"))

	s.Save("ubuntu", []jackett.Result{{Title: "New Ubuntu", InfoHash: "new1"}}, 0, false)

	if err := s.Cleanup(30 * 24 * time.Hour); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	cached, _ := s.Search("ubuntu", 0, true)
	if len(cached) != 1 {
		t.Fatalf("expected 1 result after cleanup, got %d", len(cached))
	}
	if cached[0].Title != "New Ubuntu" {
		t.Errorf("expected New Ubuntu, got %q", cached[0].Title)
	}
}

// Regression test: ensure a DB created by an OLD schema (no user_id column)
// gets migrated automatically without errors when opened by current code.
// Simulates the real-world failure: legacy DB has the `results` table without `user_id`;
// reopening with the new schema must idempotently ALTER + create the index.
// Bug caught by this: CREATE INDEX on user_id ran BEFORE the ALTER, so SQLite errored.
