package streamer

import (
	"testing"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

func newTestFavorites(t *testing.T) *FavoritesStore {
	t.Helper()
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	s, err := NewFavorites(pool)
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFavoritesAddAndList(t *testing.T) {
	f := newTestFavorites(t)
	if err := f.Add("movie A", "hashA", "magnet:A", "manual", 1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	list, _ := f.List(1, false, false)
	if len(list) != 1 {
		t.Fatalf("expected 1, got %d", len(list))
	}
	if list[0].Magnet != "magnet:A" {
		t.Errorf("magnet lost: %q", list[0].Magnet)
	}
	if list[0].UserID != 1 {
		t.Errorf("userID lost: %d", list[0].UserID)
	}
}

func TestFavoritesPerUserIsolation(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("A's movie", "hA", "magnet:A", "manual", 1)
	f.Add("B's movie", "hB", "magnet:B", "manual", 2)

	listA, _ := f.List(1, false, false)
	if len(listA) != 1 || listA[0].UserID != 1 {
		t.Errorf("user 1 leak: %v", listA)
	}
	listB, _ := f.List(2, false, false)
	if len(listB) != 1 || listB[0].UserID != 2 {
		t.Errorf("user 2 leak: %v", listB)
	}
	listAll, _ := f.List(0, true, false)
	if len(listAll) != 2 {
		t.Errorf("admin: expected 2, got %d", len(listAll))
	}
}

func TestFavoritesIsFavoriteAnyVsOf(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("movie", "h1", "m:1", "manual", 1)

	if !f.IsFavorite("movie") {
		t.Error("IsFavorite (any) should be true")
	}
	if !f.IsFavoriteOf("movie", 1) {
		t.Error("IsFavoriteOf user 1 should be true")
	}
	if f.IsFavoriteOf("movie", 2) {
		t.Error("IsFavoriteOf user 2 should be false")
	}
}

func TestFavoritesUpsertOnConflict(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("movie", "h1", "magnet:OLD", "manual", 1)
	f.Add("movie", "h1", "magnet:NEW", "auto-5min", 1)

	list, _ := f.List(1, false, false)
	if len(list) != 1 {
		t.Fatalf("expected 1 row after upsert, got %d", len(list))
	}
	if list[0].Magnet != "magnet:NEW" {
		t.Errorf("magnet not updated: %q", list[0].Magnet)
	}
	if list[0].Reason != "auto-5min" {
		t.Errorf("reason not updated: %q", list[0].Reason)
	}
}

func TestFavoritesRemoveRespectUser(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("m", "h", "magnet", "manual", 1)

	// User 2 tries to remove user 1's favorite — should not affect it
	f.Remove("m", 2, false)
	if !f.IsFavorite("m") {
		t.Error("favorite should still exist after other user tries to remove")
	}

	// User 1 removes — gone
	f.Remove("m", 1, false)
	if f.IsFavorite("m") {
		t.Error("favorite should be gone after owner removes")
	}
}

func TestFavoritesHashSetForUser(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("a", "hashA", "magnet:A", "manual", 1)
	f.Add("b", "hashB", "magnet:B", "manual", 1)
	f.Add("c", "hashC", "magnet:C", "manual", 2)

	set, err := f.HashSetForUser(1, false)
	if err != nil {
		t.Fatalf("HashSetForUser: %v", err)
	}
	if len(set) != 2 {
		t.Fatalf("expected 2 hashes for user 1, got %d", len(set))
	}
	if !set["hashA"] || !set["hashB"] {
		t.Error("missing expected hashes for user 1")
	}
	if set["hashC"] {
		t.Error("user 2 hash leaked into user 1's set")
	}
}

func TestFavoritesHashSetForUser_IncludeAll(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("a", "hashA", "magnet:A", "manual", 1)
	f.Add("b", "hashB", "magnet:B", "manual", 2)

	set, err := f.HashSetForUser(0, true)
	if err != nil {
		t.Fatalf("HashSetForUser all: %v", err)
	}
	if len(set) != 2 {
		t.Fatalf("expected 2 hashes for all users, got %d", len(set))
	}
}

func TestFavoritesHashSetForUser_NilStore(t *testing.T) {
	var f *FavoritesStore
	set, err := f.HashSetForUser(1, false)
	if err != nil {
		t.Fatalf("HashSetForUser nil: %v", err)
	}
	if len(set) != 0 {
		t.Fatalf("expected empty set, got %d", len(set))
	}
}

func TestFavoritesCreateFolder(t *testing.T) {
	f := newTestFavorites(t)
	folder, err := f.CreateFolder(1, "My Folder", nil, false)
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if folder.Name != "My Folder" {
		t.Errorf("folder name: want %q, got %q", "My Folder", folder.Name)
	}
	if folder.UserID != 1 {
		t.Errorf("user id: want 1, got %d", folder.UserID)
	}
	if folder.ParentID != nil {
		t.Errorf("expected root-level folder, got parentID=%v", *folder.ParentID)
	}
}

func TestFavoritesCreateFolder_Subfolder(t *testing.T) {
	f := newTestFavorites(t)
	parent, err := f.CreateFolder(1, "Parent", nil, false)
	if err != nil {
		t.Fatalf("CreateFolder parent: %v", err)
	}
	child, err := f.CreateFolder(1, "Child", &parent.ID, false)
	if err != nil {
		t.Fatalf("CreateFolder child: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Errorf("child parentID: want %d, got %v", parent.ID, child.ParentID)
	}
}

func TestFavoritesListFolders(t *testing.T) {
	f := newTestFavorites(t)
	f.CreateFolder(1, "Folder A", nil, false)
	f.CreateFolder(1, "Folder B", nil, false)
	f.CreateFolder(2, "Other User", nil, false)

	folders, err := f.ListFolders(1, false)
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 2 {
		t.Fatalf("expected 2 folders for user 1, got %d", len(folders))
	}
}

func TestFavoritesListFolders_NilStore(t *testing.T) {
	var f *FavoritesStore
	folders, err := f.ListFolders(1, false)
	if err != nil {
		t.Fatalf("ListFolders nil: %v", err)
	}
	if folders != nil {
		t.Fatalf("expected nil, got %+v", folders)
	}
}

func TestFavoritesRenameFolder(t *testing.T) {
	f := newTestFavorites(t)
	folder, _ := f.CreateFolder(1, "Old Name", nil, false)
	if err := f.RenameFolder(1, folder.ID, "New Name"); err != nil {
		t.Fatalf("RenameFolder: %v", err)
	}
	got, err := f.GetFolder(1, folder.ID)
	if err != nil {
		t.Fatalf("GetFolder: %v", err)
	}
	if got.Name != "New Name" {
		t.Errorf("name: want %q, got %q", "New Name", got.Name)
	}
}

func TestFavoritesDeleteFolder(t *testing.T) {
	f := newTestFavorites(t)
	folder, _ := f.CreateFolder(1, "Delete Me", nil, false)
	if err := f.DeleteFolder(1, folder.ID); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if _, err := f.GetFolder(1, folder.ID); err == nil {
		t.Fatal("expected error after DeleteFolder")
	}
}

func TestFavoritesMoveFolder_CyclePrevention(t *testing.T) {
	f := newTestFavorites(t)
	parent, _ := f.CreateFolder(1, "Parent", nil, false)
	child, _ := f.CreateFolder(1, "Child", &parent.ID, false)
	// Trying to move parent into child should reject (cycle)
	err := f.MoveFolder(1, parent.ID, &child.ID)
	if err == nil {
		t.Fatal("expected error when moving parent into child (cycle)")
	}
}

func TestFavoritesMoveFolder_ToRoot(t *testing.T) {
	f := newTestFavorites(t)
	parent, _ := f.CreateFolder(1, "Parent", nil, false)
	child, _ := f.CreateFolder(1, "Child", &parent.ID, false)
	if err := f.MoveFolder(1, child.ID, nil); err != nil {
		t.Fatalf("MoveFolder to root: %v", err)
	}
	got, _ := f.GetFolder(1, child.ID)
	if got.ParentID != nil {
		t.Errorf("expected root after move, got parentID=%v", *got.ParentID)
	}
}

func TestFavoritesMoveFavoriteToFolder(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("movie", "h1", "magnet:1", "manual", 1)
	folder, _ := f.CreateFolder(1, "My Folder", nil, false)

	if err := f.MoveFavoriteToFolder(1, "movie", &folder.ID); err != nil {
		t.Fatalf("MoveFavoriteToFolder: %v", err)
	}
	list, _ := f.List(1, false, false)
	if len(list) != 1 || list[0].FolderID == nil || *list[0].FolderID != folder.ID {
		t.Fatalf("folder assignment not persisted: %+v", list[0])
	}
}

func TestFavoritesMoveFavoriteToRoot(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("movie", "h1", "magnet:1", "manual", 1)
	folder, _ := f.CreateFolder(1, "F", nil, false)
	f.MoveFavoriteToFolder(1, "movie", &folder.ID)
	if err := f.MoveFavoriteToFolder(1, "movie", nil); err != nil {
		t.Fatalf("MoveFavoriteToFolder to root: %v", err)
	}
	list, _ := f.List(1, false, false)
	if list[0].FolderID != nil {
		t.Fatal("folder should be nil after moving to root")
	}
}

func TestFavoritesIsFavoriteByHash(t *testing.T) {
	f := newTestFavorites(t)
	if f.IsFavoriteByHash("") {
		t.Error("empty hash should not be favorited")
	}
	if f.IsFavoriteByHash("nonexistent") {
		t.Error("nonexistent hash should not be favorited")
	}
	f.Add("m", "knownHash", "magnet", "manual", 1)
	if !f.IsFavoriteByHash("knownHash") {
		t.Error("known hash should be favorited")
	}
}

func TestFavoritesIsFavorite_NilStore(t *testing.T) {
	var f *FavoritesStore
	if f.IsFavorite("anything") {
		t.Error("nil store should not report as favorite")
	}
}

func TestFavoritesIsFavoriteOf_NilStore(t *testing.T) {
	var f *FavoritesStore
	if f.IsFavoriteOf("anything", 1) {
		t.Error("nil store should not report as favorite of anyone")
	}
}

func TestFavoritesList_IncludeAll(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("a", "ha", "ma", "manual", 1)
	f.Add("b", "hb", "mb", "manual", 2)
	all, err := f.List(0, true, false)
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 favorites (all users), got %d", len(all))
	}
}

func TestFavoritesList_UsesFolderID(t *testing.T) {
	f := newTestFavorites(t)
	folder, _ := f.CreateFolder(1, "F", nil, false)
	f.Add("a", "ha", "ma", "manual", 1)
	f.MoveFavoriteToFolder(1, "a", &folder.ID)
	list, _ := f.List(1, false, false)
	if len(list) != 1 || list[0].FolderID == nil || *list[0].FolderID != folder.ID {
		t.Fatalf("folder_id not present: %+v", list[0])
	}
}

func TestFavoritesAdd_NilStore(t *testing.T) {
	var f *FavoritesStore
	err := f.Add("name", "hash", "magnet", "manual", 1)
	if err != nil {
		t.Fatalf("Add nil store should not error: %v", err)
	}
}

func TestFavoritesRemove_NilStore(t *testing.T) {
	var f *FavoritesStore
	err := f.Remove("name", 1, false)
	if err != nil {
		t.Fatalf("Remove nil store should not error: %v", err)
	}
}

func TestFavoritesCreateFolder_NilStore(t *testing.T) {
	var f *FavoritesStore
	_, err := f.CreateFolder(1, "name", nil, false)
	if err == nil {
		t.Fatal("expected error from nil store")
	}
}

func TestFavoritesRenameFolder_NilStore(t *testing.T) {
	var f *FavoritesStore
	err := f.RenameFolder(1, 1, "name")
	if err != nil {
		t.Fatalf("RenameFolder nil store should not error: %v", err)
	}
}

func TestFavoritesMoveFolder_NilStore(t *testing.T) {
	var f *FavoritesStore
	err := f.MoveFolder(1, 1, nil)
	if err != nil {
		t.Fatalf("MoveFolder nil store should not error: %v", err)
	}
}

func TestFavoritesDeleteFolder_NilStore(t *testing.T) {
	var f *FavoritesStore
	err := f.DeleteFolder(1, 1)
	if err != nil {
		t.Fatalf("DeleteFolder nil store should not error: %v", err)
	}
}

func TestFavoritesMoveFavoriteToFolder_NilStore(t *testing.T) {
	var f *FavoritesStore
	err := f.MoveFavoriteToFolder(1, "name", nil)
	if err != nil {
		t.Fatalf("MoveFavoriteToFolder nil store should not error: %v", err)
	}
}

func TestFavoritesDefaultPath(t *testing.T) {
	path := DefaultFavoritesPath("/data")
	if path != "/data/.favorites.db" {
		t.Errorf("unexpected default path: %q", path)
	}
}

func TestFavoritesRemove_IncludeAll(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("movie", "h1", "magnet:1", "manual", 1)
	f.Add("movie", "h1", "magnet:1", "manual", 2)
	f.Remove("movie", 0, true)
	if f.IsFavorite("movie") {
		t.Error("expected movie removed after includeAll=true")
	}
}

func TestFavoritesIsFavoriteOf_FailClosed(t *testing.T) {
	// Create a store on a pool, then close the pool to trigger DB errors.
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3)
	f, err := NewFavorites(pool)
	if err != nil {
		t.Fatal(err)
	}
	_ = pool.Close()
	// On a closed DB, IsFavoriteOf should return true (fail-closed)
	if !f.IsFavoriteOf("anything", 1) {
		t.Error("expected fail-closed (true) for closed DB")
	}
}

func TestFavoritesIsFavorite_FailClosed(t *testing.T) {
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3)
	f, err := NewFavorites(pool)
	if err != nil {
		t.Fatal(err)
	}
	_ = pool.Close()
	if !f.IsFavorite("anything") {
		t.Error("expected fail-closed (true) for closed DB")
	}
}

func TestFavoritesIsFavoriteByHash_FailClosed(t *testing.T) {
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3)
	f, err := NewFavorites(pool)
	if err != nil {
		t.Fatal(err)
	}
	_ = pool.Close()
	if !f.IsFavoriteByHash("anything") {
		t.Error("expected fail-closed (true) for closed DB")
	}
}

func TestFavoritesList_NilStore(t *testing.T) {
	var f *FavoritesStore
	_, err := f.List(1, false, false)
	if err == nil {
		t.Error("expected error for nil store")
	}
}

func TestListFolders_NilStore(t *testing.T) {
	var f *FavoritesStore
	folders, err := f.ListFolders(1, false)
	if err != nil {
		t.Fatal(err)
	}
	if folders != nil {
		t.Errorf("expected nil folders, got %v", folders)
	}
}

func TestListFolders_Empty(t *testing.T) {
	f := newTestFavorites(t)
	folders, err := f.ListFolders(1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 0 {
		t.Errorf("expected 0 folders, got %d", len(folders))
	}
}

func TestMoveFolder_NilStore(t *testing.T) {
	var f *FavoritesStore
	err := f.MoveFolder(1, 42, nil)
	if err != nil {
		t.Fatal("expected nil error for nil store")
	}
}

func TestMoveFolder_CycleDetection(t *testing.T) {
	f := newTestFavorites(t)

	parent, err := f.CreateFolder(1, "parent", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.CreateFolder(1, "child", &parent.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	// Moving parent into child should fail (cycle)
	newParent := child.ID
	err = f.MoveFolder(1, parent.ID, &newParent)
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestMoveFolder_MoveToSameParent(t *testing.T) {
	f := newTestFavorites(t)

	folder, err := f.CreateFolder(1, "folder", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	err = f.MoveFolder(1, folder.ID, nil)
	if err != nil {
		t.Fatalf("MoveFolder to root: %v", err)
	}
	got, err := f.GetFolder(1, folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ParentID != nil {
		t.Errorf("expected nil parent, got %d", *got.ParentID)
	}
}

func TestCreateFolder_NilStore(t *testing.T) {
	var f *FavoritesStore
	_, err := f.CreateFolder(1, "test", nil, false)
	if err == nil {
		t.Error("expected error for nil store")
	}
}

func TestRenameFolder_NilStore(t *testing.T) {
	var f *FavoritesStore
	err := f.RenameFolder(1, 42, "new")
	if err != nil {
		t.Fatal("expected nil error for nil store")
	}
}

func TestDeleteFolder_NilStore(t *testing.T) {
	var f *FavoritesStore
	err := f.DeleteFolder(1, 42)
	if err != nil {
		t.Fatal("expected nil error for nil store")
	}
}

func TestMoveFavoriteToFolder_NilStore(t *testing.T) {
	var f *FavoritesStore
	err := f.MoveFavoriteToFolder(1, "movie", nil)
	if err != nil {
		t.Fatal("expected nil error for nil store")
	}
}
