package downloads

import (
	"testing"
)

func TestNewInvalidPath(t *testing.T) {
	_, err := New("/nonexistent/path/db.sqlite")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestCreateMissingFields(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Create(Download{UserID: 1, Magnet: "m:abc"})
	if err == nil {
		t.Error("expected error for empty infoHash")
	}
	_, err = s.Create(Download{UserID: 1, InfoHash: "abc"})
	if err == nil {
		t.Error("expected error for empty magnet")
	}
}

func TestCreateUpdatesTrackerCategoryOnRequeue(t *testing.T) {
	s := newTestStore(t)

	d, _ := s.Create(Download{
		UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "m:abc", Name: "Test",
		Tracker: "old-tracker", Category: "old-cat",
	})

	_ = s.SetStatus(1, d.ID, StatusPaused)

	d2, err := s.Create(Download{
		UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "m:abc", Name: "Test",
		Tracker: "new-tracker", Category: "new-cat",
	})
	if err != nil {
		t.Fatalf("Create re-enqueue: %v", err)
	}
	if d2.Status != StatusQueued {
		t.Errorf("expected queued, got %q", d2.Status)
	}
	if d2.Tracker != "new-tracker" {
		t.Errorf("tracker: want 'new-tracker', got %q", d2.Tracker)
	}
	if d2.Category != "new-cat" {
		t.Errorf("category: want 'new-cat', got %q", d2.Category)
	}
}

func TestListFilterByStatus(t *testing.T) {
	s := newTestStore(t)

	base := Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "A"}
	d1, _ := s.Create(base)
	_, _ = s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m:b", Name: "B"})
	_ = s.SetStatus(1, d1.ID, StatusCompleted)

	list, _ := s.ListFiltered(ListFilter{UserID: 1, Status: StatusCompleted})
	if len(list) != 1 || list[0].ID != d1.ID {
		t.Errorf("expected 1 completed, got %d", len(list))
	}

	list, _ = s.ListFiltered(ListFilter{UserID: 1, Status: StatusQueued})
	if len(list) != 1 || list[0].ID != d1.ID+1 {
		t.Errorf("expected 1 queued, got %d", len(list))
	}
}

func TestListFilterByTracker(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "A", Tracker: "t1"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m:b", Name: "B", Tracker: "t2"})

	list, _ := s.ListFiltered(ListFilter{UserID: 1, Tracker: "t1"})
	if len(list) != 1 {
		t.Errorf("expected 1 result for tracker t1, got %d", len(list))
	}
}

func TestListFilterByCategory(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "A", Category: "c1"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m:b", Name: "B", Category: "c2"})

	list, _ := s.ListFiltered(ListFilter{UserID: 1, Category: "c2"})
	if len(list) != 1 {
		t.Errorf("expected 1 result for category c2, got %d", len(list))
	}
}

func TestListFilterBySearch(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "m:abc", Name: "Ubuntu Linux"})
	s.Create(Download{UserID: 1, InfoHash: "def", FileIndex: 0, Magnet: "m:def", Name: "Debian"})

	list, _ := s.ListFiltered(ListFilter{UserID: 1, Search: "Ubuntu"})
	if len(list) != 1 {
		t.Errorf("expected 1 result for 'Ubuntu', got %d", len(list))
	}

	list, _ = s.ListFiltered(ListFilter{UserID: 1, Search: "bunt"})
	if len(list) != 1 {
		t.Errorf("expected 1 result for 'bunt', got %d", len(list))
	}
}

func TestListFilterWithSortAsc(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "B"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m:b", Name: "A"})

	list, _ := s.ListFiltered(ListFilter{UserID: 1, SortCol: "name", SortDir: "asc"})
	if len(list) != 2 || list[0].Name != "A" || list[1].Name != "B" {
		t.Errorf("expected asc order: got %v", list)
	}

	list, _ = s.ListFiltered(ListFilter{UserID: 1, SortCol: "name", SortDir: "desc"})
	if len(list) != 2 || list[0].Name != "B" || list[1].Name != "A" {
		t.Errorf("expected desc order: got %v", list)
	}
}

func TestDistinctFunctionsFiltered(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "A", Tracker: "t1", Category: "c1"})
	s.Create(Download{UserID: 2, InfoHash: "b", FileIndex: 0, Magnet: "m:b", Name: "B", Tracker: "t1", Category: "c2"})
	s.Create(Download{UserID: 2, InfoHash: "c", FileIndex: 0, Magnet: "m:c", Name: "C", Tracker: "t2", Category: "c1"})

	users, _ := s.DistinctUsers()
	if len(users) != 2 {
		t.Errorf("expected 2 distinct users, got %d", len(users))
	}

	trackers, _ := s.DistinctTrackers(1)
	if len(trackers) != 1 {
		t.Errorf("expected 1 tracker for user 1, got %d", len(trackers))
	}

	cats, _ := s.DistinctCategories(1)
	if len(cats) != 1 {
		t.Errorf("expected 1 category for user 1, got %d", len(cats))
	}
}

func TestSetStatusForUserEdgeCases(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "A"})

	n, err := s.SetStatusForUser(1, StatusCompleted)
	if err != nil {
		t.Fatalf("SetStatusForUser: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row affected, got %d", n)
	}
}

func TestDeleteNotFound(t *testing.T) {
	s := newTestStore(t)
	// Delete is now IDEMPOTENT: a non-existent row is not an error (the row is
	// gone either way). This is what turns a double-click / stale-poll delete
	// from a swallowed 500 into a clean no-op.
	if err := s.Delete(1, 999); err != nil {
		t.Errorf("Delete of non-existent download must be idempotent, got: %v", err)
	}
}

func TestSetStatusByIDsMixedUser(t *testing.T) {
	s := newTestStore(t)

	d1, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "A"})
	d2, _ := s.Create(Download{UserID: 2, InfoHash: "b", FileIndex: 0, Magnet: "m:b", Name: "B"})

	_, _ = s.SetStatusByIDs(1, []int{d1.ID, d2.ID}, StatusPaused)

	got1, _ := s.Get(1, d1.ID)
	if got1.Status != StatusPaused {
		t.Errorf("expected paused for user 1's download, got %q", got1.Status)
	}

	got2, _ := s.Get(2, d2.ID)
	if got2.Status == StatusPaused {
		t.Errorf("user 2's download should not be changed")
	}
}

func TestCreateResumesFailed(t *testing.T) {
	s := newTestStore(t)

	d, _ := s.Create(Download{UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "m:abc", Name: "Test"})
	_ = s.SetError(1, d.ID, "some error")

	d2, _ := s.Create(Download{UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "m:abc", Name: "Test"})
	if d2.Status != StatusQueued {
		t.Errorf("expected queued after resume, got %q", d2.Status)
	}
	if d2.Error != "" {
		t.Errorf("error should be cleared on resume, got %q", d2.Error)
	}
}

func TestNewStoreDBError(t *testing.T) {
	_, err := New("/nonexistent\000/invalid.db")
	if err == nil {
		t.Skip("platform does not reject null byte paths")
	}
}

func TestDistinctTrackersFiltered(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "A", Tracker: "t1"})
	s.Create(Download{UserID: 2, InfoHash: "b", FileIndex: 0, Magnet: "m:b", Name: "B", Tracker: "t2"})
	s.Create(Download{UserID: 2, InfoHash: "c", FileIndex: 0, Magnet: "m:c", Name: "C", Tracker: "t3"})

	trackers, _ := s.DistinctTrackers(2)
	if len(trackers) != 2 {
		t.Errorf("expected 2 trackers for user 2, got %d", len(trackers))
	}
}

func TestDistinctCategoriesFiltered(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "A", Category: "c1"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m:b", Name: "B", Category: "c2"})
	s.Create(Download{UserID: 2, InfoHash: "c", FileIndex: 0, Magnet: "m:c", Name: "C", Category: "c3"})

	cats, _ := s.DistinctCategories(1)
	if len(cats) != 2 {
		t.Errorf("expected 2 categories for user 1, got %d", len(cats))
	}
}

func TestListFilteredAllSortedBySize(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "B", FileSize: 200})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m:b", Name: "A", FileSize: 100})

	list, _ := s.ListFilteredAll(ListFilter{SortCol: "size", SortDir: "asc"})
	if len(list) != 2 || list[0].FileSize != 100 {
		t.Errorf("expected asc by size, got %+v", list[0])
	}

	list, _ = s.ListFilteredAll(ListFilter{SortCol: "size", SortDir: "desc"})
	if len(list) != 2 || list[0].FileSize != 200 {
		t.Errorf("expected desc by size, got %+v", list[0])
	}
}

func TestListFilteredAllSortedByProgress(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "A"})

	list, _ := s.ListFilteredAll(ListFilter{SortCol: "progress", SortDir: "asc"})
	if len(list) != 1 {
		t.Errorf("expected 1 result, got %d", len(list))
	}
}

func TestListFilteredAllSortedByStatus(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "A"})

	list, _ := s.ListFilteredAll(ListFilter{SortCol: "status", SortDir: "desc"})
	if len(list) != 1 {
		t.Errorf("expected 1 result, got %d", len(list))
	}
}

func TestListFilteredAllSortedByTracker(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "A", Tracker: "t1"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m:b", Name: "B", Tracker: "t2"})

	list, _ := s.ListFilteredAll(ListFilter{SortCol: "tracker", SortDir: "asc"})
	if len(list) != 2 {
		t.Errorf("expected 2 results, got %d", len(list))
	}
}

func TestListFilteredAllSortedByCategory(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "A", Category: "c1"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m:b", Name: "B", Category: "c2"})

	list, _ := s.ListFilteredAll(ListFilter{SortCol: "category", SortDir: "asc"})
	if len(list) != 2 {
		t.Errorf("expected 2 results, got %d", len(list))
	}
}

func TestListFilteredAllSearch(t *testing.T) {
	s := newTestStore(t)

	s.Create(Download{UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "m:abc", Name: "Ubuntu"})
	s.Create(Download{UserID: 2, InfoHash: "def", FileIndex: 0, Magnet: "m:def", Name: "Debian"})

	list, _ := s.ListFilteredAll(ListFilter{Search: "Ubuntu"})
	if len(list) != 1 {
		t.Errorf("expected 1 result, got %d", len(list))
	}

	list, _ = s.ListFilteredAll(ListFilter{Search: "nonexistent"})
	if len(list) != 0 {
		t.Errorf("expected 0, got %d", len(list))
	}
}

func TestMigrateIdempotent(t *testing.T) {
	s := newTestStore(t)
	if err := s.migrate(); err != nil {
		t.Fatalf("migrate idempotent: %v", err)
	}
}

func TestSetStatusByIDsEmpty(t *testing.T) {
	s := newTestStore(t)
	n, err := s.SetStatusByIDs(1, []int{}, StatusPaused)
	if err != nil {
		t.Fatalf("SetStatusByIDs empty: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows, got %d", n)
	}
}

func TestSetStatusByIDsInvalid(t *testing.T) {
	s := newTestStore(t)
	_, err := s.SetStatusByIDs(1, []int{1}, "nonexistent")
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

func TestGetByHashNotFound(t *testing.T) {
	s := newTestStore(t)
	d, err := s.GetByKey(1, "nonexistent", 0)
	if err == nil {
		t.Fatal("expected error for non-existent download")
	}
	if d != nil {
		t.Fatal("expected nil for non-existent")
	}
}

func TestListUserNotFound(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m:a", Name: "A"})
	list, _ := s.List(2)
	if len(list) != 0 {
		t.Errorf("expected 0 results for other user, got %d", len(list))
	}
}
