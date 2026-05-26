package history

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luizg/jackui/internal/jackett"
	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSaveAndSearch(t *testing.T) {
	s := newTestStore(t)

	results := []jackett.Result{
		{Title: "Ubuntu 22.04", Tracker: "TPB", Seeders: 100, InfoHash: "abc123", MagnetURI: "magnet:?xt=urn:btih:abc123"},
		{Title: "Ubuntu 20.04", Tracker: "RARBG", Seeders: 50, InfoHash: "def456", MagnetURI: "magnet:?xt=urn:btih:def456"},
	}

	if err := s.Save("ubuntu", results, 0); err != nil {
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

	s.Save("ubuntu", []jackett.Result{r}, 0)
	s.Save("ubuntu", []jackett.Result{r}, 0) // duplicate

	cached, _ := s.Search("ubuntu", 0, true)
	if len(cached) != 1 {
		t.Fatalf("expected 1 (deduped), got %d", len(cached))
	}
}

func TestSaveEmptyInfoHash(t *testing.T) {
	s := newTestStore(t)

	r1 := jackett.Result{Title: "Ubuntu 22.04", Tracker: "A", InfoHash: ""}
	r2 := jackett.Result{Title: "Ubuntu 22.04", Tracker: "B", InfoHash: ""}

	s.Save("ubuntu", []jackett.Result{r1, r2}, 0)

	cached, _ := s.Search("ubuntu", 0, true)
	if len(cached) != 2 {
		t.Fatalf("expected 2 (no dedup without hash), got %d", len(cached))
	}
}

func TestSearchCaseInsensitive(t *testing.T) {
	s := newTestStore(t)

	s.Save("Ubuntu", []jackett.Result{
		{Title: "Ubuntu 22.04", InfoHash: "abc"},
	}, 0)

	cached, _ := s.Search("ubuntu", 0, true)
	if len(cached) != 1 {
		t.Fatalf("expected 1 result for case-insensitive match, got %d", len(cached))
	}
}

// Multi-user isolation: results saved by user A are not visible to user B (unless admin).
func TestSearchPerUserIsolation(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{{Title: "Ubuntu for A", InfoHash: "a1"}}, 1)
	s.Save("ubuntu", []jackett.Result{{Title: "Ubuntu for B", InfoHash: "b1"}}, 2)

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

	s.Save("ubuntu", []jackett.Result{{Title: "Ubuntu", InfoHash: "a1"}}, 0)
	s.Save("debian", []jackett.Result{{Title: "Debian", InfoHash: "b2"}}, 0)
	s.Save("fedora", []jackett.Result{{Title: "Fedora", InfoHash: "c3"}}, 0)

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
	}, 0)
	s.Save("better call saul", []jackett.Result{
		{Title: "Better.Call.Saul.S04.1080p", InfoHash: "bcs1"},
	}, 0)

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

	s.Save("ubuntu", []jackett.Result{{Title: "Old Ubuntu", InfoHash: "old1"}}, 0)

	s.db.Exec("UPDATE results SET saved_at = ? WHERE info_hash = 'old1'",
		time.Now().Add(-100*24*time.Hour).Format("2006-01-02 15:04:05"))

	s.Save("ubuntu", []jackett.Result{{Title: "New Ubuntu", InfoHash: "new1"}}, 0)

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
func TestMigrateLegacyDBWithoutUserID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// Step 1: hand-craft a legacy DB (no user_id column, no FTS — like a really old install)
	{
		legacy, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)")
		if err != nil {
			t.Fatalf("open legacy: %v", err)
		}
		_, err = legacy.Exec(`
			CREATE TABLE results (
				id           INTEGER PRIMARY KEY AUTOINCREMENT,
				query        TEXT NOT NULL,
				title        TEXT NOT NULL DEFAULT '',
				tracker      TEXT NOT NULL DEFAULT '',
				category     TEXT NOT NULL DEFAULT '',
				size         INTEGER NOT NULL DEFAULT 0,
				seeders      INTEGER NOT NULL DEFAULT 0,
				leechers     INTEGER NOT NULL DEFAULT 0,
				age          TEXT NOT NULL DEFAULT '',
				magnet_uri   TEXT NOT NULL DEFAULT '',
				link         TEXT NOT NULL DEFAULT '',
				info_hash    TEXT NOT NULL DEFAULT '',
				publish_date TEXT NOT NULL DEFAULT '',
				saved_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
			INSERT INTO results(query, title, info_hash) VALUES('old', 'Legacy Movie', 'oldhash');
		`)
		if err != nil {
			t.Fatalf("seed legacy: %v", err)
		}
		legacy.Close()
	}

	// Step 2: re-open with the current (new schema) code — must NOT panic, NOT error.
	s, err := New(path)
	if err != nil {
		t.Fatalf("re-open after legacy schema: %v", err)
	}
	defer s.Close()

	// Step 3: legacy row preserved (default user_id=0); FTS rebuilt
	if !s.hasColumn("results", "user_id") {
		t.Fatal("user_id column not added by migration")
	}

	// Step 4: new inserts with user_id work
	if err := s.Save("new", []jackett.Result{{Title: "Fresh", InfoHash: "newhash"}}, 5); err != nil {
		t.Fatalf("Save after migration: %v", err)
	}
	r, _ := s.Search("new", 5, false)
	if len(r) != 1 {
		t.Errorf("expected 1 result for user 5, got %d", len(r))
	}
}

func TestNewInvalidPath(t *testing.T) {
	_, err := New("/nonexistent/path/db.sqlite")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
	_ = os.Remove("/nonexistent/path/db.sqlite")
}
