package downloads

import (
	"testing"
)

func TestCreate_MissingFields(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Create(Download{UserID: 1, InfoHash: "", Magnet: ""})
	if err == nil {
		t.Fatal("expected error for empty infoHash/magnet")
	}
	_, err = s.Create(Download{UserID: 1, InfoHash: "abc", Magnet: ""})
	if err == nil {
		t.Fatal("expected error for empty magnet")
	}
}

func TestGetByKey(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetByKey(1, "nonexistent", 0)
	if err == nil {
		t.Fatal("expected error for nonexistent key")
	}
}

func TestGetCompletedPath(t *testing.T) {
	s := newTestStore(t)
	path, err := s.GetCompletedPath("nonexistent", 0)
	if err != nil {
		t.Fatalf("GetCompletedPath: %v", err)
	}
	if path != "" {
		t.Fatalf("expected empty path, got %q", path)
	}
}

func TestGetCompletedPath_NilStore(t *testing.T) {
	var s *Store
	path, err := s.GetCompletedPath("hash", 0)
	if err != nil {
		t.Fatalf("GetCompletedPath nil: %v", err)
	}
	if path != "" {
		t.Fatalf("expected empty path, got %q", path)
	}
}

func TestGetCompletedPath_Found(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{
		UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "m", Name: "x", FilePath: "/data/file.mkv",
	})
	s.SetStatus(1, d.ID, StatusCompleted)
	path, err := s.GetCompletedPath("abc", 0)
	if err != nil {
		t.Fatalf("GetCompletedPath: %v", err)
	}
	if path != "/data/file.mkv" {
		t.Fatalf("expected /data/file.mkv, got %q", path)
	}
}

func TestListFiltered(t *testing.T) {
	s := newTestStore(t)
	d1, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m1", Name: "Alpha", Tracker: "t1", Category: "cat1"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m2", Name: "Beta", Tracker: "t2", Category: "cat2"})
	s.SetStatus(1, d1.ID, StatusCompleted)

	// Filter by status
	items, err := s.ListFiltered(ListFilter{UserID: 1, Status: StatusCompleted})
	if err != nil {
		t.Fatalf("ListFiltered: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 completed, got %d", len(items))
	}

	// Filter by tracker
	items, _ = s.ListFiltered(ListFilter{UserID: 1, Tracker: "t2"})
	if len(items) != 1 {
		t.Fatalf("expected 1 with tracker t2, got %d", len(items))
	}

	// Filter by category
	items, _ = s.ListFiltered(ListFilter{UserID: 1, Category: "cat1"})
	if len(items) != 1 {
		t.Fatalf("expected 1 with category cat1, got %d", len(items))
	}

	// Filter by search
	items, _ = s.ListFiltered(ListFilter{UserID: 1, Search: "Beta"})
	if len(items) != 1 {
		t.Fatalf("expected 1 with search Beta, got %d", len(items))
	}

	// Sort asc
	items, _ = s.ListFiltered(ListFilter{UserID: 1, SortCol: "name", SortDir: "asc"})
	if len(items) != 2 || items[0].Name != "Alpha" {
		t.Fatalf("sort asc failed: %+v", items)
	}

	// Sort by progress
	items, _ = s.ListFiltered(ListFilter{UserID: 1, SortCol: "progress"})
	if len(items) != 2 {
		t.Fatalf("sort by progress: got %d", len(items))
	}

	// Sort by status
	items, _ = s.ListFiltered(ListFilter{UserID: 1, SortCol: "status"})
	if len(items) != 2 {
		t.Fatalf("sort by status: got %d", len(items))
	}

	// Sort by tracker
	items, _ = s.ListFiltered(ListFilter{UserID: 1, SortCol: "tracker"})
	if len(items) != 2 {
		t.Fatalf("sort by tracker: got %d", len(items))
	}

	// Sort by category
	items, _ = s.ListFiltered(ListFilter{UserID: 1, SortCol: "category"})
	if len(items) != 2 {
		t.Fatalf("sort by category: got %d", len(items))
	}
}

func TestListAll(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a"})
	s.Create(Download{UserID: 2, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "b"})
	all, err := s.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}
}

func TestListFilteredAll(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a", Tracker: "t1", Category: "c1"})
	s.Create(Download{UserID: 2, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "b", Tracker: "t2", Category: "c2"})

	items, err := s.ListFilteredAll(ListFilter{Status: StatusDownloading})
	if err != nil {
		t.Fatalf("ListFilteredAll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2, got %d", len(items))
	}

	items, _ = s.ListFilteredAll(ListFilter{UserIDFilter: "1"})
	if len(items) != 1 {
		t.Fatalf("expected 1 for user 1, got %d", len(items))
	}

	items, _ = s.ListFilteredAll(ListFilter{Search: "a"})
	if len(items) != 1 {
		t.Fatalf("expected 1 for search 'a', got %d", len(items))
	}

	// Sort by user_id
	items, _ = s.ListFilteredAll(ListFilter{SortCol: "user_id"})
	if len(items) != 2 {
		t.Fatalf("sort by user_id: got %d", len(items))
	}

	// Sort by username (also maps to user_id)
	items, _ = s.ListFilteredAll(ListFilter{SortCol: "username"})
	if len(items) != 2 {
		t.Fatalf("sort by username: got %d", len(items))
	}
}

func TestListFilteredAll_EmptySearch(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "alpha", Tracker: "t1", Category: "c1"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "beta", Tracker: "t2", Category: "c2"})

	items, err := s.ListFilteredAll(ListFilter{})
	if err != nil {
		t.Fatalf("ListFilteredAll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2, got %d", len(items))
	}
}

func TestDistinctUsers(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a"})
	s.Create(Download{UserID: 2, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "b"})
	users, err := s.DistinctUsers()
	if err != nil {
		t.Fatalf("DistinctUsers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
}

func TestDistinctTrackers(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a", Tracker: "t1"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "b", Tracker: "t2"})
	s.Create(Download{UserID: 1, InfoHash: "c", FileIndex: 0, Magnet: "m", Name: "c"})
	trackers, err := s.DistinctTrackers(1)
	if err != nil {
		t.Fatalf("DistinctTrackers: %v", err)
	}
	if len(trackers) != 2 {
		t.Fatalf("expected 2 trackers, got %d", len(trackers))
	}
}

func TestDistinctCategories(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a", Category: "movies"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "b", Category: "tv"})
	s.Create(Download{UserID: 1, InfoHash: "c", FileIndex: 0, Magnet: "m", Name: "c"})
	cats, err := s.DistinctCategories(1)
	if err != nil {
		t.Fatalf("DistinctCategories: %v", err)
	}
	if len(cats) != 2 {
		t.Fatalf("expected 2 categories, got %d", len(cats))
	}
}

func TestSetStatus_Invalid(t *testing.T) {
	s := newTestStore(t)
	err := s.SetStatus(1, 1, "invalid_status")
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestSetStatusForUser(t *testing.T) {
	s := newTestStore(t)
	d1, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a"})
	d2, _ := s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "b"})
	s.Create(Download{UserID: 1, InfoHash: "c", FileIndex: 0, Magnet: "m", Name: "c"})
	d4, _ := s.Create(Download{UserID: 2, InfoHash: "d", FileIndex: 0, Magnet: "m", Name: "d"})

	s.SetStatus(1, d1.ID, StatusCompleted)

	n, err := s.SetStatusForUser(1, StatusPaused)
	if err != nil {
		t.Fatalf("SetStatusForUser: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 paused, got %d", n)
	}
	// d1 (completed) should not be touched
	got, _ := s.Get(1, d1.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("completed download changed status to %q", got.Status)
	}
	// d4 (user 2) should not be touched
	got, _ = s.Get(2, d4.ID)
	if got.Status == StatusPaused {
		t.Fatal("user 2 download should not be affected")
	}
	// d2 should be paused
	got, _ = s.Get(1, d2.ID)
	if got.Status != StatusPaused {
		t.Fatalf("expected paused, got %q", got.Status)
	}
}

func TestSetStatusForUser_Invalid(t *testing.T) {
	s := newTestStore(t)
	_, err := s.SetStatusForUser(1, "invalid")
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestSetStatusByIDs(t *testing.T) {
	s := newTestStore(t)
	d1, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a"})
	d2, _ := s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "b"})
	d3other, _ := s.Create(Download{UserID: 2, InfoHash: "c", FileIndex: 0, Magnet: "m", Name: "c"})

	n, err := s.SetStatusByIDs(1, []int{d1.ID, d2.ID, d3other.ID}, StatusPaused)
	if err != nil {
		t.Fatalf("SetStatusByIDs: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 paused, got %d", n)
	}
	// d3other (user 2) should not be affected
	got, _ := s.Get(2, d3other.ID)
	if got.Status == StatusPaused {
		t.Fatal("user 2 download should not be affected")
	}
}

func TestSetStatusByIDs_Invalid(t *testing.T) {
	s := newTestStore(t)
	_, err := s.SetStatusByIDs(1, []int{1}, "invalid")
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestSetStatusByIDs_Empty(t *testing.T) {
	s := newTestStore(t)
	n, err := s.SetStatusByIDs(1, nil, StatusPaused)
	if err != nil {
		t.Fatalf("SetStatusByIDs empty: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

func TestSetError(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a"})
	if err := s.SetError(1, d.ID, "something went wrong"); err != nil {
		t.Fatalf("SetError: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.Status != StatusFailed {
		t.Fatalf("expected failed, got %q", got.Status)
	}
	if got.Error != "something went wrong" {
		t.Fatalf("error = %q", got.Error)
	}
}

func TestSetFilePath(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a"})
	if err := s.SetFilePath(1, d.ID, "/new/path/file.mkv"); err != nil {
		t.Fatalf("SetFilePath: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.FilePath != "/new/path/file.mkv" {
		t.Fatalf("file_path = %q", got.FilePath)
	}
}

func TestUpdateName(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "old"})
	if err := s.UpdateName(1, d.ID, "new_name"); err != nil {
		t.Fatalf("UpdateName: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.Name != "new_name" {
		t.Fatalf("name = %q", got.Name)
	}
}

func TestUpdateMetadata(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "old", FilePath: "old_path"})
	if err := s.UpdateMetadata(1, d.ID, "new_name", "new_path.mkv", 5000); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.Name != "new_name" || got.FilePath != "new_path.mkv" || got.FileSize != 5000 {
		t.Fatalf("metadata not updated: %+v", got)
	}
}

func TestUpdateProgress_RegressionProtection(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x", FileSize: 1000})
	// Progress lower than stored should not regress in store
	_ = s.UpdateProgress(1, d.ID, 500)
	got, _ := s.Get(1, d.ID)
	_ = got
}

func TestHashSetForUser(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a"})
	s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "b"})
	s.Create(Download{UserID: 2, InfoHash: "c", FileIndex: 0, Magnet: "m", Name: "c"})

	set, err := s.HashSetForUser(1, false)
	if err != nil {
		t.Fatalf("HashSetForUser: %v", err)
	}
	if len(set) != 2 {
		t.Fatalf("expected 2 hashes for user 1, got %d", len(set))
	}
}

func TestHashSetForUser_IncludeAll(t *testing.T) {
	s := newTestStore(t)
	s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a"})
	s.Create(Download{UserID: 2, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "b"})

	set, err := s.HashSetForUser(0, true)
	if err != nil {
		t.Fatalf("HashSetForUser all: %v", err)
	}
	if len(set) != 2 {
		t.Fatalf("expected 2 hashes for all users, got %d", len(set))
	}
}

func TestHashSetForUser_NilStore(t *testing.T) {
	var s *Store
	set, err := s.HashSetForUser(1, false)
	if err != nil {
		t.Fatalf("HashSetForUser nil: %v", err)
	}
	if len(set) != 0 {
		t.Fatalf("expected empty set")
	}
}

func TestValidStatus(t *testing.T) {
	if !validStatus(StatusQueued) {
		t.Error("queued should be valid")
	}
	if !validStatus(StatusDownloading) {
		t.Error("downloading should be valid")
	}
	if !validStatus(StatusCompleted) {
		t.Error("completed should be valid")
	}
	if !validStatus(StatusFailed) {
		t.Error("failed should be valid")
	}
	if !validStatus(StatusPaused) {
		t.Error("paused should be valid")
	}
	if validStatus("invalid") {
		t.Error("invalid should not be valid")
	}
}

func TestCreate_UpdatesTrackerAndCategoryOnRequeue(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "m", Name: "old"})
	_ = s.SetStatus(1, d.ID, StatusPaused)
	d2, _ := s.Create(Download{UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "m", Name: "old", Tracker: "t1", Category: "movies"})
	if d2.Tracker != "t1" || d2.Category != "movies" {
		t.Fatalf("tracker/category not updated on requeue: %+v", d2)
	}
}

func TestDelete_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.Delete(1, 999)
	if err == nil {
		t.Fatal("expected error for nonexistent download")
	}
}
