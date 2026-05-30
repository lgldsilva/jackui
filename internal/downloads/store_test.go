package downloads

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "downloads.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	d, err := s.Create(Download{
		UserID:    1,
		InfoHash:  "abc",
		FileIndex: 0,
		FilePath:  "Movie/movie.mkv",
		FileSize:  1000,
		Name:      "Movie",
		Magnet:    "magnet:?xt=urn:btih:abc",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if d.Status != StatusDownloading {
		t.Fatalf("expected status=downloading, got %q", d.Status)
	}
	got, err := s.Get(1, d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Movie" {
		t.Fatalf("name mismatch: %q", got.Name)
	}
}

func TestCreateIdempotent(t *testing.T) {
	s := newTestStore(t)
	base := Download{
		UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:abc",
		Name: "x", FilePath: "x", FileSize: 10,
	}
	d1, err := s.Create(base)
	if err != nil {
		t.Fatalf("Create#1: %v", err)
	}
	d2, err := s.Create(base)
	if err != nil {
		t.Fatalf("Create#2: %v", err)
	}
	if d1.ID != d2.ID {
		t.Fatalf("expected same ID on re-create, got %d vs %d", d1.ID, d2.ID)
	}
}

func TestPauseResumeFlowsThroughCreate(t *testing.T) {
	s := newTestStore(t)
	base := Download{
		UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:abc",
		Name: "x", FilePath: "x", FileSize: 10,
	}
	d, err := s.Create(base)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetStatus(1, d.ID, StatusPaused); err != nil {
		t.Fatalf("SetStatus pause: %v", err)
	}
	// Re-create should resume the paused entry, not error out.
	d2, err := s.Create(base)
	if err != nil {
		t.Fatalf("Create resume: %v", err)
	}
	if d2.Status != StatusDownloading {
		t.Fatalf("expected resumed entry to be downloading, got %q", d2.Status)
	}
}

// A user must not be able to mutate another user's download by guessing its ID.
func TestSetStatusIsUserScoped(t *testing.T) {
	s := newTestStore(t)
	owned, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a"})

	// User 2 tries to pause user 1's download — the WHERE user_id guard makes it
	// a no-op (no error, zero rows), so the status is unchanged.
	if err := s.SetStatus(2, owned.ID, StatusPaused); err != nil {
		t.Fatalf("SetStatus (wrong user): %v", err)
	}
	got, _ := s.Get(1, owned.ID)
	if got.Status == StatusPaused {
		t.Fatal("cross-user SetStatus mutated another user's download")
	}
	// Same for progress.
	_ = s.UpdateProgress(2, owned.ID, 999)
	got, _ = s.Get(1, owned.ID)
	if got.BytesDownloaded == 999 {
		t.Fatal("cross-user UpdateProgress mutated another user's download")
	}
}

func TestUpdateProgressAndComplete(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{
		UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "m", Name: "x", FileSize: 1000,
	})
	if err := s.UpdateProgress(1, d.ID, 500); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.BytesDownloaded != 500 || got.Progress != 0.5 {
		t.Fatalf("progress not tracked: %+v", got)
	}
	if err := s.SetStatus(1, d.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}
	got, _ = s.Get(1, d.ID)
	if got.Status != StatusCompleted || got.CompletedAt == nil {
		t.Fatalf("expected completed with timestamp, got %+v", got)
	}
}

func TestListActiveOnlyDownloading(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a"})
	b, _ := s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "b"})
	c, _ := s.Create(Download{UserID: 1, InfoHash: "c", FileIndex: 0, Magnet: "m", Name: "c"})
	_ = s.SetStatus(1, b.ID, StatusPaused)
	_ = s.SetStatus(1, c.ID, StatusCompleted)
	active, err := s.ListActive()
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 1 || active[0].ID != a.ID {
		t.Fatalf("expected only #%d active, got %+v", a.ID, active)
	}
}

func TestDeleteOwnership(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x"})
	// Wrong user — should fail
	if err := s.Delete(99, d.ID); err == nil {
		t.Fatal("expected Delete to fail with wrong user")
	}
	if err := s.Delete(1, d.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(1, d.ID); err == nil {
		t.Fatal("expected Get after Delete to fail")
	}
}

func TestGetCompletedPath_Found(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "comp-hash", FileIndex: 0, Magnet: "m", Name: "x", FilePath: "/done/movie.mkv"})
	s.SetStatus(1, d.ID, StatusCompleted)
	path, err := s.GetCompletedPath("comp-hash", 0)
	if err != nil {
		t.Fatalf("GetCompletedPath: %v", err)
	}
	if path != "/done/movie.mkv" {
		t.Errorf("path = %q, want %q", path, "/done/movie.mkv")
	}
}

func TestGetCompletedPath_NotFound(t *testing.T) {
	s := newTestStore(t)
	path, err := s.GetCompletedPath("nonexistent", 0)
	if err != nil {
		t.Fatalf("GetCompletedPath: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
}

func TestGetCompletedPath_NilStore(t *testing.T) {
	var nilS *Store
	path, err := nilS.GetCompletedPath("hash", 0)
	if err != nil {
		t.Fatalf("GetCompletedPath nil: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
}

func TestHashSetForUser(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "h1", FileIndex: 0, Magnet: "m1", Name: "a"})
	s.Create(Download{UserID: 1, InfoHash: "h2", FileIndex: 0, Magnet: "m2", Name: "b"})
	s.Create(Download{UserID: 2, InfoHash: "h3", FileIndex: 0, Magnet: "m3", Name: "c"})

	set, err := s.HashSetForUser(1, false)
	if err != nil {
		t.Fatalf("HashSetForUser: %v", err)
	}
	if len(set) != 2 {
		t.Errorf("expected 2 hashes for user 1, got %d", len(set))
	}
	if !set["h1"] || !set["h2"] {
		t.Error("missing expected hashes for user 1")
	}
	if set["h3"] {
		t.Error("user 1 should not see h3")
	}
}

func TestHashSetForUser_IncludeAll(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "h1", FileIndex: 0, Magnet: "m1", Name: "a"})
	s.Create(Download{UserID: 2, InfoHash: "h2", FileIndex: 0, Magnet: "m2", Name: "b"})

	set, err := s.HashSetForUser(0, true)
	if err != nil {
		t.Fatalf("HashSetForUser all: %v", err)
	}
	if len(set) != 2 {
		t.Errorf("expected 2 hashes with includeAll, got %d", len(set))
	}
}

func TestHashSetForUser_NilStore(t *testing.T) {
	var nilS *Store
	set, err := nilS.HashSetForUser(1, false)
	if err != nil {
		t.Fatalf("HashSetForUser nil: %v", err)
	}
	if len(set) != 0 {
		t.Errorf("expected empty set, got %d", len(set))
	}
}

func TestList(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "first"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "second"})
	s.Create(Download{UserID: 2, InfoHash: "c", FileIndex: 0, Magnet: "m", Name: "other"})

	list, err := s.List(1)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 downloads for user 1, got %d", len(list))
	}
}

func TestListAll(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x"})
	s.Create(Download{UserID: 2, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "y"})

	all, err := s.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 downloads, got %d", len(all))
	}
}

func TestListFiltered(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "Alpha", Tracker: "t1", Category: "movies"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "Beta", Tracker: "t2", Category: "tv"})

	tests := []struct {
		name      string
		filter    ListFilter
		wantLen   int
		wantFirst string
	}{
		{"status downloading", ListFilter{UserID: 1, Status: StatusDownloading}, 2, ""},
		{"tracker t1", ListFilter{UserID: 1, Tracker: "t1"}, 1, "Alpha"},
		{"category tv", ListFilter{UserID: 1, Category: "tv"}, 1, "Beta"},
		{"search Alpha", ListFilter{UserID: 1, Search: "Alpha"}, 1, ""},
		{"sort name asc", ListFilter{UserID: 1, SortCol: "name", SortDir: "asc"}, 2, "Alpha"},
		{"sort status asc", ListFilter{UserID: 1, SortCol: "status", SortDir: "asc"}, 2, ""},
		{"sort size desc", ListFilter{UserID: 1, SortCol: "size", SortDir: "desc"}, 2, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filtered, err := s.ListFiltered(tc.filter)
			if err != nil {
				t.Fatalf("ListFiltered: %v", err)
			}
			if len(filtered) != tc.wantLen {
				t.Errorf("expected %d results, got %d", tc.wantLen, len(filtered))
			}
			if tc.wantFirst != "" && len(filtered) > 0 && filtered[0].Name != tc.wantFirst {
				t.Errorf("expected first %q, got %q", tc.wantFirst, filtered[0].Name)
			}
		})
	}
}

func TestListFilteredAll(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x", Tracker: "t1", Category: "movies"})
	s.Create(Download{UserID: 2, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "y", Tracker: "t2", Category: "tv"})

	all, err := s.ListFilteredAll(ListFilter{UserIDFilter: "1"})
	if err != nil {
		t.Fatalf("ListFilteredAll: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 download for user 1, got %d", len(all))
	}

	all, err = s.ListFilteredAll(ListFilter{})
	if err != nil {
		t.Fatalf("ListFilteredAll empty: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 downloads, got %d", len(all))
	}

	all, err = s.ListFilteredAll(ListFilter{Status: StatusDownloading})
	if err != nil {
		t.Fatalf("ListFilteredAll status: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 downloading, got %d", len(all))
	}

	all, err = s.ListFilteredAll(ListFilter{Search: "x"})
	if err != nil {
		t.Fatalf("ListFilteredAll search: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 search hit, got %d", len(all))
	}

	all, err = s.ListFilteredAll(ListFilter{SortCol: "name", SortDir: "asc"})
	if err != nil {
		t.Fatalf("ListFilteredAll sort name: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 results, got %d", len(all))
	}

	for _, sortCol := range []string{"size", "progress", "status", "tracker", "category", "user_id"} {
		all, err = s.ListFilteredAll(ListFilter{SortCol: sortCol})
		if err != nil {
			t.Fatalf("ListFilteredAll sort %q: %v", sortCol, err)
		}
		if len(all) != 2 {
			t.Errorf("sort %q: expected 2 results, got %d", sortCol, len(all))
		}
	}
}

func TestDistinctUsers(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x"})
	s.Create(Download{UserID: 2, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "y"})

	users, err := s.DistinctUsers()
	if err != nil {
		t.Fatalf("DistinctUsers: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("expected 2 users, got %d", len(users))
	}
}

func TestDistinctTrackers(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x", Tracker: "t1"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "y", Tracker: "t2"})

	trackers, err := s.DistinctTrackers(1)
	if err != nil {
		t.Fatalf("DistinctTrackers: %v", err)
	}
	if len(trackers) != 2 {
		t.Errorf("expected 2 trackers, got %d", len(trackers))
	}
}

func TestDistinctCategories(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x", Category: "movies"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "y", Category: "tv"})

	cats, err := s.DistinctCategories(1)
	if err != nil {
		t.Fatalf("DistinctCategories: %v", err)
	}
	if len(cats) != 2 {
		t.Errorf("expected 2 categories, got %d", len(cats))
	}
}

func TestSetStatusForUser(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "y"})

	n, err := s.SetStatusForUser(1, StatusPaused)
	if err != nil {
		t.Fatalf("SetStatusForUser: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 rows affected, got %d", n)
	}

	got, _ := s.List(1)
	if got[0].Status != StatusPaused {
		t.Errorf("expected paused, got %q", got[0].Status)
	}
}

func TestSetStatusForUser_InvalidStatus(t *testing.T) {
	s := newTestStore(t)
	_, err := s.SetStatusForUser(1, "bogus")
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestSetStatusByIDs(t *testing.T) {
	s := newTestStore(t)
	d1, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x"})
	d2, _ := s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "y"})

	n, err := s.SetStatusByIDs(1, []int{d1.ID, d2.ID}, StatusPaused)
	if err != nil {
		t.Fatalf("SetStatusByIDs: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 rows affected, got %d", n)
	}
}

func TestSetStatusByIDs_Empty(t *testing.T) {
	s := newTestStore(t)
	n, err := s.SetStatusByIDs(1, []int{}, StatusPaused)
	if err != nil {
		t.Fatalf("SetStatusByIDs empty: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows, got %d", n)
	}
}

func TestSetStatusByIDs_InvalidStatus(t *testing.T) {
	s := newTestStore(t)
	_, err := s.SetStatusByIDs(1, []int{1}, "bogus")
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestSetError(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x"})

	err := s.SetError(1, d.ID, "something went wrong")
	if err != nil {
		t.Fatalf("SetError: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.Status != StatusFailed {
		t.Errorf("expected failed status, got %q", got.Status)
	}
	if got.Error != "something went wrong" {
		t.Errorf("expected error msg, got %q", got.Error)
	}
}

func TestSetFilePath(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x"})

	if err := s.SetFilePath(1, d.ID, "/new/path/file.mkv"); err != nil {
		t.Fatalf("SetFilePath: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.FilePath != "/new/path/file.mkv" {
		t.Errorf("expected new path, got %q", got.FilePath)
	}
}

func TestUpdateName(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "old-name"})

	if err := s.UpdateName(1, d.ID, "new-name"); err != nil {
		t.Fatalf("UpdateName: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.Name != "new-name" {
		t.Errorf("expected 'new-name', got %q", got.Name)
	}
}

func TestUpdateMetadata(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x"})

	if err := s.UpdateMetadata(1, d.ID, "new-name", "/path/file.mkv", 5000); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.Name != "new-name" || got.FilePath != "/path/file.mkv" || got.FileSize != 5000 {
		t.Errorf("metadata not updated: %+v", got)
	}
}

func TestValidStatus(t *testing.T) {
	for _, s := range []string{StatusQueued, StatusDownloading, StatusCompleted, StatusFailed, StatusPaused} {
		if !validStatus(s) {
			t.Errorf("validStatus(%q) should be true", s)
		}
	}
	if validStatus("bogus") {
		t.Error("validStatus('bogus') should be false")
	}
}

func TestSetStatus_Invalid(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x"})
	err := s.SetStatus(1, d.ID, "bogus")
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestCreate_MissingFields(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Create(Download{UserID: 1})
	if err == nil {
		t.Fatal("expected error for missing infoHash/magnet")
	}
}

func TestNilStore_SafeCalls(t *testing.T) {
	var nilS *Store
	if _, err := nilS.HashSetForUser(1, false); err != nil {
		t.Errorf("HashSetForUser on nil: %v", err)
	}
}
