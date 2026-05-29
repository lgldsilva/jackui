package streamer

import (
	"path/filepath"
	"testing"
)

func TestFavoritesHashSetForUser_Empty(t *testing.T) {
	f := newTestFavorites(t)
	set, err := f.HashSetForUser(1, false)
	if err != nil {
		t.Fatalf("HashSetForUser: %v", err)
	}
	if len(set) != 0 {
		t.Errorf("expected empty set, got %d", len(set))
	}
}

func TestFavoritesHashSetForUser_WithEntries(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("movieA", "hashA", "magnet:A", "manual", 1)
	f.Add("movieB", "hashB", "magnet:B", "manual", 1)
	f.Add("movieC", "hashC", "magnet:C", "manual", 2)

	set, err := f.HashSetForUser(1, false)
	if err != nil {
		t.Fatalf("HashSetForUser: %v", err)
	}
	if len(set) != 2 {
		t.Errorf("expected 2 hashes for user 1, got %d", len(set))
	}
	if !set["hashA"] || !set["hashB"] {
		t.Error("missing expected hashes for user 1")
	}
	if set["hashC"] {
		t.Error("user 1 should not see hashC")
	}
}

func TestFavoritesHashSetForUser_IncludeAll(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("movieA", "hashA", "magnet:A", "manual", 1)
	f.Add("movieB", "hashB", "magnet:B", "manual", 2)

	set, err := f.HashSetForUser(0, true)
	if err != nil {
		t.Fatalf("HashSetForUser includeAll: %v", err)
	}
	if len(set) != 2 {
		t.Errorf("expected 2 hashes with includeAll, got %d", len(set))
	}
}

func TestFavoritesHashSetForUser_NilStore(t *testing.T) {
	var nilFav *FavoritesStore
	set, err := nilFav.HashSetForUser(1, false)
	if err != nil {
		t.Fatalf("HashSetForUser nil: %v", err)
	}
	if len(set) != 0 {
		t.Errorf("expected empty set from nil store, got %d", len(set))
	}
}

func TestFavoritesIsFavoriteByHash(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("movie", "myhash123", "magnet:1", "manual", 1)

	if !f.IsFavoriteByHash("myhash123") {
		t.Error("IsFavoriteByHash should be true for existing hash")
	}
	if f.IsFavoriteByHash("nonexistent") {
		t.Error("IsFavoriteByHash should be false for nonexistent hash")
	}
}

func TestFavoritesIsFavoriteByHash_Empty(t *testing.T) {
	f := newTestFavorites(t)
	if f.IsFavoriteByHash("") {
		t.Error("IsFavoriteByHash('') should be false")
	}
}

func TestFavoritesIsFavoriteByHash_NilStore(t *testing.T) {
	var nilFav *FavoritesStore
	if nilFav.IsFavoriteByHash("hash") {
		t.Error("IsFavoriteByHash on nil store should be false")
	}
}

func TestDefaultFavoritesPath(t *testing.T) {
	got := DefaultFavoritesPath("/data/streams")
	want := "/data/streams/.favorites.db"
	if got != want {
		t.Errorf("DefaultFavoritesPath: got %q, want %q", got, want)
	}
}

func TestFavoritesNilReceiver(t *testing.T) {
	var nilFav *FavoritesStore

	if nilFav.Add("x", "h", "m", "manual", 1) != nil {
		t.Error("Add on nil store should return nil")
	}
	if nilFav.Remove("x", 1, false) != nil {
		t.Error("Remove on nil store should return nil")
	}
	if nilFav.IsFavorite("x") {
		t.Error("IsFavorite on nil store should be false")
	}
	if nilFav.IsFavoriteOf("x", 1) {
		t.Error("IsFavoriteOf on nil store should be false")
	}
	if _, err := nilFav.List(1, false); err == nil {
		t.Error("List on nil store should return error")
	}
}

func TestFavoritesListFolders_Empty(t *testing.T) {
	f := newTestFavorites(t)
	folders, err := f.ListFolders(1)
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 0 {
		t.Errorf("expected 0 folders, got %d", len(folders))
	}
}

func TestFavoritesCreateAndListFolders(t *testing.T) {
	f := newTestFavorites(t)

	fl, err := f.CreateFolder(1, "Movies", nil)
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if fl.Name != "Movies" || fl.UserID != 1 {
		t.Errorf("unexpected folder: %+v", fl)
	}
	if fl.ID <= 0 {
		t.Errorf("expected positive ID, got %d", fl.ID)
	}
	if fl.ParentID != nil {
		t.Errorf("expected nil parent for root folder, got %v", fl.ParentID)
	}

	folders, err := f.ListFolders(1)
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 1 || folders[0].Name != "Movies" {
		t.Errorf("ListFolders: got %+v", folders)
	}

	f2, err := f.CreateFolder(1, "Action", &fl.ID)
	if err != nil {
		t.Fatalf("CreateFolder sub: %v", err)
	}
	if f2.ParentID == nil || *f2.ParentID != fl.ID {
		t.Errorf("subfolder parent: got %v, want %d", f2.ParentID, fl.ID)
	}
}

func TestFavoritesGetFolder(t *testing.T) {
	f := newTestFavorites(t)
	fl, _ := f.CreateFolder(1, "Series", nil)

	got, err := f.GetFolder(1, fl.ID)
	if err != nil {
		t.Fatalf("GetFolder: %v", err)
	}
	if got.Name != "Series" {
		t.Errorf("GetFolder name = %q", got.Name)
	}

	if _, err := f.GetFolder(1, 99999); err == nil {
		t.Error("GetFolder nonexistent should error")
	}
}

func TestFavoritesRenameFolder(t *testing.T) {
	f := newTestFavorites(t)
	fl, _ := f.CreateFolder(1, "Old", nil)

	if err := f.RenameFolder(1, fl.ID, "New"); err != nil {
		t.Fatalf("RenameFolder: %v", err)
	}
	got, _ := f.GetFolder(1, fl.ID)
	if got.Name != "New" {
		t.Errorf("after rename: name = %q, want 'New'", got.Name)
	}
}

func TestFavoritesMoveFolder_ToRoot(t *testing.T) {
	f := newTestFavorites(t)
	parent, _ := f.CreateFolder(1, "Parent", nil)
	child, _ := f.CreateFolder(1, "Child", &parent.ID)

	if err := f.MoveFolder(1, child.ID, nil); err != nil {
		t.Fatalf("MoveFolder to root: %v", err)
	}
	got, _ := f.GetFolder(1, child.ID)
	if got.ParentID != nil {
		t.Errorf("expected nil parent after move to root, got %v", got.ParentID)
	}
}

func TestFavoritesMoveFolder_CycleDetection(t *testing.T) {
	f := newTestFavorites(t)
	a, _ := f.CreateFolder(1, "A", nil)
	b, _ := f.CreateFolder(1, "B", &a.ID)

	err := f.MoveFolder(1, a.ID, &b.ID)
	if err == nil {
		t.Fatal("expected error when moving folder into its descendant")
	}
}

func TestFavoritesDeleteFolder(t *testing.T) {
	f := newTestFavorites(t)
	fl, _ := f.CreateFolder(1, "ToDelete", nil)

	if err := f.DeleteFolder(1, fl.ID); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	folders, _ := f.ListFolders(1)
	if len(folders) != 0 {
		t.Errorf("expected 0 folders after delete, got %d", len(folders))
	}
}

func TestFavoritesMoveFavoriteToFolder(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("movie", "hash1", "magnet:1", "manual", 1)
	fl, _ := f.CreateFolder(1, "Folder", nil)

	if err := f.MoveFavoriteToFolder(1, "movie", &fl.ID); err != nil {
		t.Fatalf("MoveFavoriteToFolder: %v", err)
	}

	list, _ := f.List(1, false)
	if len(list) != 1 || list[0].FolderID == nil || *list[0].FolderID != fl.ID {
		t.Errorf("after move: FolderID=%v, want %d", list[0].FolderID, fl.ID)
	}

	if err := f.MoveFavoriteToFolder(1, "movie", nil); err != nil {
		t.Fatalf("MoveFavoriteToFolder to root: %v", err)
	}
	list, _ = f.List(1, false)
	if list[0].FolderID != nil {
		t.Errorf("expected nil FolderID after move to root, got %v", list[0].FolderID)
	}
}

func TestFavoritesFoldersNilReceiver(t *testing.T) {
	var nilFav *FavoritesStore

	if _, err := nilFav.ListFolders(1); err != nil {
		t.Errorf("ListFolders on nil: %v", err)
	}
	if _, err := nilFav.CreateFolder(1, "x", nil); err == nil {
		t.Error("CreateFolder on nil should error")
	}
	// GetFolder doesn't handle nil receiver — tested separately when store is open
	if nilFav.RenameFolder(1, 1, "x") != nil {
		t.Error("RenameFolder on nil should return nil")
	}
	if nilFav.MoveFolder(1, 1, nil) != nil {
		t.Error("MoveFolder on nil should return nil")
	}
	if nilFav.DeleteFolder(1, 1) != nil {
		t.Error("DeleteFolder on nil should return nil")
	}
	if nilFav.MoveFavoriteToFolder(1, "x", nil) != nil {
		t.Error("MoveFavoriteToFolder on nil should return nil")
	}
}

func TestFavoritesIsFavoriteDBErrorFailClosed(t *testing.T) {
	f := newTestFavorites(t)
	f.Close()
	if !f.IsFavorite("anything") {
		t.Error("IsFavorite should fail-closed (return true) on closed DB")
	}
}

func TestFavoritesListByUserLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fav_legacy2.db")
	{
		older, err := NewFavorites(path)
		if err != nil {
			t.Fatalf("first open: %v", err)
		}
		older.Add("legacy-item", "oldhash", "", "manual", 0)
		older.Close()
	}

	f, err := NewFavorites(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer f.Close()

	listAll, _ := f.List(0, true)
	if len(listAll) != 1 {
		t.Errorf("expected 1 legacy item, got %d", len(listAll))
	}
	listUser0, _ := f.List(0, false)
	if len(listUser0) != 1 {
		t.Errorf("expected 1 item for user 0, got %d", len(listUser0))
	}
}
