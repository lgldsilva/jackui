package playlists

import (
	"path/filepath"
	"testing"
)

func plwStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "plw.db"))
	if err != nil {
		t.Fatalf("plw New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// New should fail when the path is not openable (a directory, not a file).
func Test_plwNewBadPath(t *testing.T) {
	if s, err := New(t.TempDir()); err == nil {
		if s != nil {
			s.Close()
		}
		t.Fatal("plw: expected error opening a directory as a db file")
	}
}

// Create with empty name is rejected before touching the db.
func Test_plwCreateEmptyName(t *testing.T) {
	s := plwStore(t)
	if _, err := s.Create(1, "", "desc"); err == nil {
		t.Fatal("plw: expected error for empty name")
	}
}

// Get with includeAll=true ignores ownership; missing id returns nil,nil.
func Test_plwGetIncludeAllAndMissing(t *testing.T) {
	s := plwStore(t)
	p, _ := s.Create(7, "owned", "")

	// Other user via admin/includeAll can still fetch.
	got, err := s.Get(p.ID, 999, true)
	if err != nil {
		t.Fatalf("plw Get includeAll: %v", err)
	}
	if got == nil || got.ID != p.ID || got.Name != "owned" {
		t.Fatalf("plw: expected playlist via includeAll, got %v", got)
	}

	// Missing id => nil, nil (sql.ErrNoRows path).
	miss, err := s.Get(424242, 7, false)
	if err != nil {
		t.Fatalf("plw Get missing err: %v", err)
	}
	if miss != nil {
		t.Fatalf("plw: expected nil for missing playlist, got %v", miss)
	}

	// Wrong owner, not admin => nil.
	wrong, err := s.Get(p.ID, 8, false)
	if err != nil {
		t.Fatalf("plw Get wrong owner err: %v", err)
	}
	if wrong != nil {
		t.Fatalf("plw: expected nil for wrong owner, got %v", wrong)
	}
}

// Update / Delete on a non-existent playlist must return errPlaylistNotFound.
func Test_plwUpdateDeleteNotFound(t *testing.T) {
	s := plwStore(t)
	if err := s.Update(555, 1, "x", "y", false); err == nil {
		t.Fatal("plw: expected error updating missing playlist")
	}
	if err := s.Delete(555, 1, false); err == nil {
		t.Fatal("plw: expected error deleting missing playlist")
	}
	// includeAll path on missing id also errors (RowsAffected==0).
	if err := s.Update(555, 0, "x", "y", true); err == nil {
		t.Fatal("plw: expected error updating missing playlist (includeAll)")
	}
	if err := s.Delete(555, 0, true); err == nil {
		t.Fatal("plw: expected error deleting missing playlist (includeAll)")
	}
}

// AddItem validation: missing magnet/title rejected; on a non-owned playlist rejected.
func Test_plwAddItemValidation(t *testing.T) {
	s := plwStore(t)
	p, _ := s.Create(1, "P", "")

	if _, err := s.AddItem(p.ID, 1, Item{Title: "", Magnet: "m:x"}, false); err == nil {
		t.Error("plw: expected error for empty title")
	}
	if _, err := s.AddItem(p.ID, 1, Item{Title: "T", Magnet: ""}, false); err == nil {
		t.Error("plw: expected error for empty magnet")
	}
	if _, err := s.AddItem(99999, 1, Item{Title: "T", Magnet: "m:x"}, false); err == nil {
		t.Error("plw: expected error adding to missing playlist")
	}
}

// AddItem with a LibraryID set exercises the non-nil libraryIDArg branch and
// is read back through getItem (LibraryID populated).
func Test_plwAddItemWithLibraryID(t *testing.T) {
	s := plwStore(t)
	p, _ := s.Create(1, "P", "")
	lib := 42
	it, err := s.AddItem(p.ID, 1, Item{Title: "T", Magnet: "m:x", LibraryID: &lib, FileIndex: 3}, false)
	if err != nil {
		t.Fatalf("plw AddItem: %v", err)
	}
	if it.LibraryID == nil || *it.LibraryID != 42 {
		t.Fatalf("plw: expected LibraryID 42, got %v", it.LibraryID)
	}
	if it.FileIndex != 3 {
		t.Fatalf("plw: expected FileIndex 3, got %d", it.FileIndex)
	}

	// Items read-back also populates LibraryID (Items scan branch).
	items, _ := s.Items(p.ID, 1, false)
	if len(items) != 1 || items[0].LibraryID == nil || *items[0].LibraryID != 42 {
		t.Fatalf("plw: Items did not return LibraryID, got %v", items)
	}
}

// Items on a non-owned playlist errors.
func Test_plwItemsNotOwned(t *testing.T) {
	s := plwStore(t)
	p, _ := s.Create(1, "P", "")
	if _, err := s.Items(p.ID, 2, false); err == nil {
		t.Error("plw: expected error listing items of non-owned playlist")
	}
	// includeAll lets an admin list (empty but no error).
	if _, err := s.Items(p.ID, 0, true); err != nil {
		t.Errorf("plw: admin Items should not error: %v", err)
	}
}

// RemoveItem: missing item id, and non-owned playlist.
func Test_plwRemoveItemErrors(t *testing.T) {
	s := plwStore(t)
	p, _ := s.Create(1, "P", "")
	s.AddItem(p.ID, 1, Item{Title: "A", Magnet: "m:a"}, false)

	if err := s.RemoveItem(p.ID, 999999, 1, false); err == nil {
		t.Error("plw: expected error removing missing item")
	}
	if err := s.RemoveItem(p.ID, 1, 2, false); err == nil {
		t.Error("plw: expected error removing from non-owned playlist")
	}
}

// Reorder: not-owned, missing item, and the curPos==newPos early return.
func Test_plwReorderEdgeCases(t *testing.T) {
	s := plwStore(t)
	p, _ := s.Create(1, "P", "")
	a, _ := s.AddItem(p.ID, 1, Item{Title: "A", Magnet: "m:a"}, false)

	if err := s.Reorder(p.ID, a.ID, 2, 0, false); err == nil {
		t.Error("plw: expected error reordering non-owned playlist")
	}
	if err := s.Reorder(p.ID, 999999, 1, 0, false); err == nil {
		t.Error("plw: expected error reordering missing item")
	}
	// Same position => no-op, no error.
	if err := s.Reorder(p.ID, a.ID, 1, a.Position, false); err != nil {
		t.Errorf("plw: same-position reorder should be no-op, got %v", err)
	}
}

// ownsPlaylist includeAll branch (admin) returns true for any existing id.
func Test_plwOwnsPlaylistIncludeAll(t *testing.T) {
	s := plwStore(t)
	p, _ := s.Create(1, "P", "")
	if !s.ownsPlaylist(p.ID, 0, true) {
		t.Error("plw: admin includeAll should own existing playlist")
	}
	if s.ownsPlaylist(999999, 0, true) {
		t.Error("plw: admin should not own missing playlist")
	}
}

// getItem on a missing id returns an error.
func Test_plwGetItemMissing(t *testing.T) {
	s := plwStore(t)
	if _, err := s.getItem(987654); err == nil {
		t.Error("plw: expected error from getItem on missing id")
	}
}
