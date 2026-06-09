package streamer

import "testing"

// HiddenHashSet returns the info_hashes of favourites living in a hidden folder
// — the set used to filter Continue Watching and the downloads list.
func TestHiddenHashSet(t *testing.T) {
	f := newTestFavorites(t)
	hidden, err := f.CreateFolder(1, "Secret", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add("secret", "habc", "magnet:s", "manual", 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Add("public", "hpub", "magnet:p", "manual", 1); err != nil {
		t.Fatal(err)
	}
	if err := f.MoveFavoriteToFolder(1, "secret", &hidden.ID); err != nil {
		t.Fatal(err)
	}

	set, err := f.HiddenHashSet(1, false)
	if err != nil {
		t.Fatal(err)
	}
	if !set["habc"] {
		t.Error("hidden-folder hash should be in the set")
	}
	if set["hpub"] {
		t.Error("public hash must NOT be in the set")
	}

	// Un-hiding the folder empties the set.
	if err := f.SetFolderHidden(1, hidden.ID, false); err != nil {
		t.Fatal(err)
	}
	set, _ = f.HiddenHashSet(1, false)
	if len(set) != 0 {
		t.Errorf("after un-hide, set should be empty, got %d", len(set))
	}
}

// A nil store returns an empty set, never panics (handlers rely on this).
func TestHiddenHashSet_NilStore(t *testing.T) {
	var f *FavoritesStore
	set, err := f.HiddenHashSet(1, false)
	if err != nil || len(set) != 0 {
		t.Errorf("nil store: got set=%v err=%v", set, err)
	}
}

// SetLocalPathHidden + HiddenLocalPaths round-trip: hide, list, unhide.
func TestHiddenLocalPaths(t *testing.T) {
	f := newTestFavorites(t)

	if err := f.SetLocalPathHidden(1, "GDrive", "secret/dir", true); err != nil {
		t.Fatal(err)
	}
	// Idempotent: hiding again is a no-op.
	if err := f.SetLocalPathHidden(1, "GDrive", "secret/dir", true); err != nil {
		t.Fatal(err)
	}
	// A different user's path is isolated.
	if err := f.SetLocalPathHidden(2, "GDrive", "other/dir", true); err != nil {
		t.Fatal(err)
	}

	paths, err := f.HiddenLocalPaths(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0].Mount != "GDrive" || paths[0].Path != "secret/dir" {
		t.Fatalf("expected user 1's single hidden path, got %+v", paths)
	}

	// Unhide removes it.
	if err := f.SetLocalPathHidden(1, "GDrive", "secret/dir", false); err != nil {
		t.Fatal(err)
	}
	paths, _ = f.HiddenLocalPaths(1)
	if len(paths) != 0 {
		t.Errorf("after unhide, expected 0 paths, got %d", len(paths))
	}
}

func TestHiddenLocalPaths_NilStore(t *testing.T) {
	var f *FavoritesStore
	if paths, err := f.HiddenLocalPaths(1); err != nil || paths != nil {
		t.Errorf("nil store: got paths=%v err=%v", paths, err)
	}
	if err := f.SetLocalPathHidden(1, "m", "p", true); err == nil {
		t.Error("nil store SetLocalPathHidden should error")
	}
}
