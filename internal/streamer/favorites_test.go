package streamer

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestFavorites(t *testing.T) *FavoritesStore {
	t.Helper()
	s, err := NewFavorites(filepath.Join(t.TempDir(), "fav.db"))
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
	list, _ := f.List(1, false)
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

	listA, _ := f.List(1, false)
	if len(listA) != 1 || listA[0].UserID != 1 {
		t.Errorf("user 1 leak: %v", listA)
	}
	listB, _ := f.List(2, false)
	if len(listB) != 1 || listB[0].UserID != 2 {
		t.Errorf("user 2 leak: %v", listB)
	}
	listAll, _ := f.List(0, true)
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

	list, _ := f.List(1, false)
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

// Regression: legacy favorites DB (no user_id/magnet) must migrate without errors.
// Captures the bug where CREATE INDEX on user_id ran before ALTER added the column.
func TestFavoritesMigrateLegacyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fav_legacy.db")
	{
		legacy, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)")
		if err != nil {
			t.Fatalf("open legacy: %v", err)
		}
		_, err = legacy.Exec(`
			CREATE TABLE favorites (
				name TEXT PRIMARY KEY,
				info_hash TEXT,
				favorited_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				reason TEXT NOT NULL DEFAULT 'manual'
			);
			INSERT INTO favorites(name, info_hash, reason) VALUES('old fav', 'oldhash', 'manual');
		`)
		if err != nil {
			t.Fatalf("seed legacy: %v", err)
		}
		legacy.Close()
	}

	f, err := NewFavorites(path)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	defer f.Close()

	if !f.hasColumn("favorites", "user_id") {
		t.Error("user_id column not added")
	}
	if !f.hasColumn("favorites", "magnet") {
		t.Error("magnet column not added")
	}

	// Legacy row still queryable (with default user_id=0)
	list, _ := f.List(0, false)
	if len(list) != 1 {
		t.Errorf("expected legacy row preserved, got %d rows", len(list))
	}

	// New favorites work after migration
	if err := f.Add("new fav", "newhash", "magnet:new", "manual", 5); err != nil {
		t.Fatalf("Add after migration: %v", err)
	}
	list5, _ := f.List(5, false)
	if len(list5) != 1 || list5[0].Magnet != "magnet:new" {
		t.Errorf("new favorite not isolated to user 5: %v", list5)
	}
}

// Regression: the PlayerModal favoriteAdd args-order bug wrote the literal "manual" into the
// magnet column. NewFavorites must repair those rows on open by reconstructing the magnet from
// info_hash. Idempotent — a subsequent reopen finds zero rows to fix.
func TestFavoritesRecoversManualMagnetCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fav_corrupt.db")

	// First open creates the schema. Seed corrupted rows directly via SQL to simulate prod state.
	{
		f, err := NewFavorites(path)
		if err != nil {
			t.Fatalf("first open: %v", err)
		}
		// Bypass Add() (which sanitises) and inject the corruption shape.
		_, err = f.db.Exec(
			`INSERT INTO favorites(name, info_hash, magnet, reason, user_id) VALUES
				('Corrupt Movie',  'aabbccddeeff00112233445566778899aabbccdd', 'manual', 'manual', 1),
				('No Hash',        '',                                          'manual', 'manual', 1),
				('Healthy',        '1234567890abcdef1234567890abcdef12345678', 'magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678', 'manual', 1)`,
		)
		if err != nil {
			t.Fatalf("seed corrupt rows: %v", err)
		}
		f.Close()
	}

	// Reopen — the recovery UPDATE must rewrite the "manual" magnet into a proper one for
	// rows that have an info_hash. Rows without info_hash stay as "manual" (defensive UI catches them).
	f, err := NewFavorites(path)
	if err != nil {
		t.Fatalf("reopen with recovery: %v", err)
	}
	defer f.Close()

	rows, _ := f.List(1, false)
	got := map[string]string{}
	for _, r := range rows {
		got[r.Name] = r.Magnet
	}
	wantCorruptFixed := "magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd"
	if got["Corrupt Movie"] != wantCorruptFixed {
		t.Errorf("corrupt row not repaired:\n  got  %q\n  want %q", got["Corrupt Movie"], wantCorruptFixed)
	}
	if got["No Hash"] != "manual" {
		// Defensive: can't reconstruct a magnet without an info_hash. Leave it; UI will warn.
		t.Errorf("row without info_hash should be untouched, got %q", got["No Hash"])
	}
	if got["Healthy"] != "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678" {
		t.Errorf("healthy row mutated: %q", got["Healthy"])
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
	folder, err := f.CreateFolder(1, "My Folder", nil)
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
	parent, err := f.CreateFolder(1, "Parent", nil)
	if err != nil {
		t.Fatalf("CreateFolder parent: %v", err)
	}
	child, err := f.CreateFolder(1, "Child", &parent.ID)
	if err != nil {
		t.Fatalf("CreateFolder child: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Errorf("child parentID: want %d, got %v", parent.ID, child.ParentID)
	}
}

func TestFavoritesListFolders(t *testing.T) {
	f := newTestFavorites(t)
	f.CreateFolder(1, "Folder A", nil)
	f.CreateFolder(1, "Folder B", nil)
	f.CreateFolder(2, "Other User", nil)

	folders, err := f.ListFolders(1)
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 2 {
		t.Fatalf("expected 2 folders for user 1, got %d", len(folders))
	}
}

func TestFavoritesListFolders_NilStore(t *testing.T) {
	var f *FavoritesStore
	folders, err := f.ListFolders(1)
	if err != nil {
		t.Fatalf("ListFolders nil: %v", err)
	}
	if folders != nil {
		t.Fatalf("expected nil, got %+v", folders)
	}
}

func TestFavoritesRenameFolder(t *testing.T) {
	f := newTestFavorites(t)
	folder, _ := f.CreateFolder(1, "Old Name", nil)
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
	folder, _ := f.CreateFolder(1, "Delete Me", nil)
	if err := f.DeleteFolder(1, folder.ID); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if _, err := f.GetFolder(1, folder.ID); err == nil {
		t.Fatal("expected error after DeleteFolder")
	}
}

func TestFavoritesMoveFolder_CyclePrevention(t *testing.T) {
	f := newTestFavorites(t)
	parent, _ := f.CreateFolder(1, "Parent", nil)
	child, _ := f.CreateFolder(1, "Child", &parent.ID)
	// Trying to move parent into child should reject (cycle)
	err := f.MoveFolder(1, parent.ID, &child.ID)
	if err == nil {
		t.Fatal("expected error when moving parent into child (cycle)")
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
		t.Errorf("expected root after move, got parentID=%v", *got.ParentID)
	}
}

func TestFavoritesMoveFavoriteToFolder(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("movie", "h1", "magnet:1", "manual", 1)
	folder, _ := f.CreateFolder(1, "My Folder", nil)

	if err := f.MoveFavoriteToFolder(1, "movie", &folder.ID); err != nil {
		t.Fatalf("MoveFavoriteToFolder: %v", err)
	}
	list, _ := f.List(1, false)
	if len(list) != 1 || list[0].FolderID == nil || *list[0].FolderID != folder.ID {
		t.Fatalf("folder assignment not persisted: %+v", list[0])
	}
}

func TestFavoritesMoveFavoriteToRoot(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("movie", "h1", "magnet:1", "manual", 1)
	folder, _ := f.CreateFolder(1, "F", nil)
	f.MoveFavoriteToFolder(1, "movie", &folder.ID)
	if err := f.MoveFavoriteToFolder(1, "movie", nil); err != nil {
		t.Fatalf("MoveFavoriteToFolder to root: %v", err)
	}
	list, _ := f.List(1, false)
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
	all, err := f.List(0, true)
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 favorites (all users), got %d", len(all))
	}
}

func TestFavoritesList_UsesFolderID(t *testing.T) {
	f := newTestFavorites(t)
	folder, _ := f.CreateFolder(1, "F", nil)
	f.Add("a", "ha", "ma", "manual", 1)
	f.MoveFavoriteToFolder(1, "a", &folder.ID)
	list, _ := f.List(1, false)
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
	_, err := f.CreateFolder(1, "name", nil)
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

func TestNewFavorites_InvalidPath(t *testing.T) {
	_, err := NewFavorites("/nonexistent/foo/fav.db")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestFavoritesHasColumn(t *testing.T) {
	f := newTestFavorites(t)
	if !f.hasColumn("favorites", "name") {
		t.Error("expected 'name' column to exist")
	}
	if f.hasColumn("favorites", "nonexistent_column_xyz") {
		t.Error("expected nonexistent column to not exist")
	}
}

func TestFavoritesHasColumn_Error(t *testing.T) {
	f, err := NewFavorites(filepath.Join(t.TempDir(), "fav.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Query a non-existent table
	if f.hasColumn("nonexistent_table", "col") {
		t.Error("expected false for non-existent table")
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
	// Create a store then close it to trigger DB error
	f, err := NewFavorites(filepath.Join(t.TempDir(), "fav.db"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	// On a closed DB, IsFavoriteOf should return true (fail-closed)
	if !f.IsFavoriteOf("anything", 1) {
		t.Error("expected fail-closed (true) for closed DB")
	}
}

func TestFavoritesIsFavorite_FailClosed(t *testing.T) {
	f, err := NewFavorites(filepath.Join(t.TempDir(), "fav.db"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if !f.IsFavorite("anything") {
		t.Error("expected fail-closed (true) for closed DB")
	}
}

func TestFavoritesIsFavoriteByHash_FailClosed(t *testing.T) {
	f, err := NewFavorites(filepath.Join(t.TempDir(), "fav.db"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if !f.IsFavoriteByHash("anything") {
		t.Error("expected fail-closed (true) for closed DB")
	}
}

func TestFavoritesList_NilStore(t *testing.T) {
	var f *FavoritesStore
	_, err := f.List(1, false)
	if err == nil {
		t.Error("expected error for nil store")
	}
}
