package history

import (
	"testing"

	"github.com/lgldsilva/jackui/internal/jackett"
)

func TestDeleteIncognito(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{{Title: "Ubuntu Public", InfoHash: "pub1"}}, 1, false)
	s.Save("debian", []jackett.Result{{Title: "Debian Secret", InfoHash: "sec1"}}, 1, true)

	if err := s.DeleteIncognito(1); err != nil {
		t.Fatalf("DeleteIncognito: %v", err)
	}

	// Only the public entry should remain
	all, _ := s.Search("ubuntu", 1, true)
	if len(all) != 1 {
		t.Errorf("expected 1 public result, got %d", len(all))
	}
}

func TestDeleteIncognitoNilStore(t *testing.T) {
	var s *Store
	if err := s.DeleteIncognito(1); err != nil {
		t.Fatalf("nil DeleteIncognito: %v", err)
	}
}

func TestDeleteIncognitoOtherUser(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{{Title: "User1 Secret", InfoHash: "u1s"}}, 1, true)
	s.Save("ubuntu", []jackett.Result{{Title: "User2 Secret", InfoHash: "u2s"}}, 2, true)

	if err := s.DeleteIncognito(2); err != nil {
		t.Fatalf("DeleteIncognito: %v", err)
	}

	// User 1's incognito should survive — check via direct DB query
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM results WHERE user_id = 1 AND incognito = 1").Scan(&count)
	if count != 1 {
		t.Errorf("expected user1's incognito to survive, got %d rows", count)
	}

	var count2 int
	s.db.QueryRow("SELECT COUNT(*) FROM results WHERE user_id = 2 AND incognito = 1").Scan(&count2)
	if count2 != 0 {
		t.Errorf("expected user2's incognito to be deleted, got %d rows", count2)
	}
}

func TestDeleteAllIncognito(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{{Title: "Public", InfoHash: "pub1"}}, 1, false)
	s.Save("debian", []jackett.Result{{Title: "Secret1", InfoHash: "sec1"}}, 1, true)
	s.Save("fedora", []jackett.Result{{Title: "Secret2", InfoHash: "sec2"}}, 2, true)

	if err := s.DeleteAllIncognito(); err != nil {
		t.Fatalf("DeleteAllIncognito: %v", err)
	}

	all, _ := s.Search("ubuntu", 1, true)
	if len(all) != 1 {
		t.Errorf("expected 1 public result, got %d", len(all))
	}
}

func TestDeleteAllIncognitoNilStore(t *testing.T) {
	var s *Store
	if err := s.DeleteAllIncognito(); err != nil {
		t.Fatalf("nil DeleteAllIncognito: %v", err)
	}
}

func TestGetResult(t *testing.T) {
	s := newTestStore(t)

	err := s.Save("ubuntu", []jackett.Result{
		{Title: "Ubuntu 22.04", Tracker: "TPB", Seeders: 100, InfoHash: "abc123", MagnetURI: "magnet:?xt=urn:btih:abc123"},
	}, 1, false)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Get the saved result
	results, _ := s.Search("ubuntu", 1, false)
	if len(results) != 1 {
		t.Fatalf("expected 1 saved result, got %d", len(results))
	}
	savedID := results[0].ID

	r, err := s.GetResult(savedID, 1, false)
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if r.Title != "Ubuntu 22.04" {
		t.Errorf("title: got %q", r.Title)
	}
	if !r.Cached {
		t.Error("expected Cached=true")
	}

	// Other user should not see it
	_, err = s.GetResult(savedID, 2, false)
	if err == nil {
		t.Error("expected error for other user")
	}

	// Admin should see it
	r, err = s.GetResult(savedID, 0, true)
	if err != nil {
		t.Fatalf("GetResult admin: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil result for admin")
	}
}

func TestGetResultNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetResult(999, 1, false)
	if err == nil {
		t.Error("expected error for non-existent result")
	}
}

func TestUpdateSeedersLeechers(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{
		{Title: "Ubuntu", InfoHash: "abc", Seeders: 10, Leechers: 5},
	}, 1, false)

	results, _ := s.Search("ubuntu", 1, false)
	if len(results) != 1 {
		t.Fatalf("expected 1 result")
	}
	id := results[0].ID

	if err := s.UpdateSeedersLeechers(id, 50, 20); err != nil {
		t.Fatalf("UpdateSeedersLeechers: %v", err)
	}

	updated, _ := s.GetResult(id, 1, false)
	if updated.Seeders != 50 || updated.Leechers != 20 {
		t.Errorf("expected (50,20), got (%d,%d)", updated.Seeders, updated.Leechers)
	}
}

func TestDeleteQuery(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{
		{Title: "Ubuntu A", InfoHash: "a1"},
		{Title: "Ubuntu B", InfoHash: "b1"},
	}, 1, false)
	s.Save("debian", []jackett.Result{
		{Title: "Debian", InfoHash: "c1"},
	}, 1, false)

	if err := s.DeleteQuery("ubuntu", 1, false); err != nil {
		t.Fatalf("DeleteQuery: %v", err)
	}

	r, _ := s.Search("ubuntu", 1, false)
	if len(r) != 0 {
		t.Errorf("expected 0 ubuntu results, got %d", len(r))
	}

	r, _ = s.Search("debian", 1, false)
	if len(r) != 1 {
		t.Errorf("expected 1 debian result, got %d", len(r))
	}
}

func TestDeleteQueryAdmin(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{{Title: "A", InfoHash: "a1"}}, 1, false)
	s.Save("ubuntu", []jackett.Result{{Title: "B", InfoHash: "b1"}}, 2, false)

	if err := s.DeleteQuery("ubuntu", 0, true); err != nil {
		t.Fatalf("DeleteQuery admin: %v", err)
	}

	r, _ := s.Search("ubuntu", 1, true)
	if len(r) != 0 {
		t.Errorf("expected 0 results after admin delete, got %d", len(r))
	}
}

func TestDeleteAll(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{{Title: "U", InfoHash: "u1"}}, 1, false)
	s.Save("debian", []jackett.Result{{Title: "D", InfoHash: "d1"}}, 2, false)

	if err := s.DeleteAll(1, false); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	r, _ := s.Search("ubuntu", 1, false)
	if len(r) != 0 {
		t.Errorf("expected 0 for user 1, got %d", len(r))
	}

	r, _ = s.Search("debian", 2, false)
	if len(r) != 1 {
		t.Errorf("expected 1 for user 2, got %d", len(r))
	}
}

func TestDeleteAllAdmin(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{{Title: "U", InfoHash: "u1"}}, 1, false)
	s.Save("debian", []jackett.Result{{Title: "D", InfoHash: "d1"}}, 2, false)

	if err := s.DeleteAll(0, true); err != nil {
		t.Fatalf("DeleteAll admin: %v", err)
	}

	r, _ := s.Search("ubuntu", 1, true)
	if len(r) != 0 {
		t.Errorf("expected 0 after admin wipe, got %d", len(r))
	}
}

func TestBuildFTSQueryEmpty(t *testing.T) {
	if q := buildFTSQuery(""); q != "" {
		t.Errorf("expected empty, got %q", q)
	}
}

func TestBuildFTSQueryStripsQuotes(t *testing.T) {
	q := buildFTSQuery(`hello "world"`)
	if q != `hello:* & world:*` {
		t.Errorf("expected sanitized, got %q", q)
	}
}

func TestBuildFTSQueryHandlesOnlySpecial(t *testing.T) {
	q := buildFTSQuery(`"`)
	if q != "" {
		t.Errorf("expected empty for only quotes, got %q", q)
	}
}

func TestSearchAllEmptyQuery(t *testing.T) {
	s := newTestStore(t)
	r, err := s.SearchAll("", 10, 0, true)
	if err != nil {
		t.Fatalf("SearchAll empty: %v", err)
	}
	if len(r) != 0 {
		t.Errorf("expected 0, got %d", len(r))
	}
}

func TestSaveEmptyResults(t *testing.T) {
	s := newTestStore(t)
	if err := s.Save("ubuntu", nil, 0, false); err != nil {
		t.Fatalf("Save nil: %v", err)
	}
	if err := s.Save("ubuntu", []jackett.Result{}, 0, false); err != nil {
		t.Fatalf("Save empty: %v", err)
	}
}

func TestIncognitoExcludedFromSearch(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{{Title: "Public", InfoHash: "pub1"}}, 1, false)
	s.Save("ubuntu", []jackett.Result{{Title: "Secret", InfoHash: "sec1"}}, 1, true)

	r, _ := s.Search("ubuntu", 1, false)
	if len(r) != 1 || r[0].Title != "Public" {
		t.Errorf("incognito leaked into search: got %v", r)
	}
}

func TestIncognitoExcludedFromRecentEntries(t *testing.T) {
	s := newTestStore(t)

	s.Save("ubuntu", []jackett.Result{{Title: "Public", InfoHash: "pub1"}}, 1, false)
	s.Save("secret", []jackett.Result{{Title: "Secret", InfoHash: "sec1"}}, 1, true)

	entries, _ := s.RecentEntries(10, 1, false)
	for _, e := range entries {
		if e.Query == "secret" {
			t.Error("incognito query leaked into RecentEntries")
		}
	}
}

func TestSearchAllDedupByHash(t *testing.T) {
	s := newTestStore(t)

	s.Save("query1", []jackett.Result{{Title: "Same Content", InfoHash: "dup123"}}, 1, false)
	s.Save("query2", []jackett.Result{{Title: "Same Content", InfoHash: "dup123"}}, 1, false)

	r, _ := s.SearchAll("Same", 10, 1, false)
	if len(r) != 1 {
		t.Errorf("expected 1 deduped result, got %d", len(r))
	}
}

func TestSearchAllLimitDefault(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 300; i++ {
		hash := string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
		s.Save("test", []jackett.Result{
			{Title: "Item", InfoHash: hash},
		}, 1, false)
	}

	r, _ := s.SearchAll("Item", 0, 1, false)
	if len(r) > 200 {
		t.Errorf("expected capped at 200, got %d", len(r))
	}
}
