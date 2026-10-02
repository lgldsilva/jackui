package streamer

import "testing"

// HiddenFavorites returns the identity set (hashes + names) of favourites
// living in a hidden folder — the set used to filter Continue Watching, the
// downloads list and search enrichment.
func TestHiddenFavorites(t *testing.T) {
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

	set, err := f.HiddenFavorites(1, false)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Hashes["habc"] {
		t.Error("hidden-folder hash should be in the set")
	}
	if !set.Names["secret"] {
		t.Error("hidden-folder name should be in the set")
	}
	if set.Hashes["hpub"] || set.Names["public"] {
		t.Error("public favourite must NOT be in the set")
	}

	// Un-hiding the folder empties the set.
	if err := f.SetFolderHidden(1, hidden.ID, false); err != nil {
		t.Fatal(err)
	}
	set, _ = f.HiddenFavorites(1, false)
	if !set.Empty() {
		t.Errorf("after un-hide, set should be empty, got %v", set)
	}
}

// THE Continue-Watching regression: a hidden favourite without an info_hash
// (quick-favourite whose link resolution failed) must still enter the curtain
// — by NAME — and a hash stored with unusual casing must normalize into the
// set, since the library joins on lowercase hex.
func TestHiddenFavorites_NameOnlyAndCasing(t *testing.T) {
	f := newTestFavorites(t)
	hidden, err := f.CreateFolder(1, "Secret", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	// Hash-less: hiding is a per-title decision.
	if err := f.Add("Some.Movie.2023.1080p", "", "", "manual", 1); err != nil {
		t.Fatal(err)
	}
	// Uppercase/padded hash: must land normalized.
	if err := f.Add("Other Movie", "  ABCDEF0123456789ABCDEF0123456789ABCDEF012  ", "magnet:o", "manual", 1); err != nil {
		t.Fatal(err)
	}
	if err := f.MoveFavoriteToFolder(1, "Some.Movie.2023.1080p", &hidden.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.MoveFavoriteToFolder(1, "Other Movie", &hidden.ID); err != nil {
		t.Fatal(err)
	}

	set, err := f.HiddenFavorites(1, false)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Names["some.movie.2023.1080p"] {
		t.Errorf("hash-less hidden favourite missing from names: %v", set.Names)
	}
	if set.Hashes[""] {
		t.Error("empty hash must not enter the hash set")
	}
	if !set.Hashes["abcdef0123456789abcdef0123456789abcdef012"] {
		t.Errorf("uppercase/padded hash not normalized into the set: %v", set.Hashes)
	}
	if !set.Names["other movie"] {
		t.Errorf("name not normalized (trim) into the set: %v", set.Names)
	}
}

// Subfolders of a hidden folder inherit the veil: the old direct-membership
// query leaked favourites parked one level down.
func TestHiddenFavorites_SubfolderRecursion(t *testing.T) {
	f := newTestFavorites(t)
	parent, err := f.CreateFolder(1, "Hidden root", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.CreateFolder(1, "Plain subfolder", &parent.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add("nested", "nnnn", "magnet:n", "manual", 1); err != nil {
		t.Fatal(err)
	}
	if err := f.MoveFavoriteToFolder(1, "nested", &child.ID); err != nil {
		t.Fatal(err)
	}

	set, err := f.HiddenFavorites(1, false)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Hashes["nnnn"] || !set.Names["nested"] {
		t.Errorf("favourite in a subfolder of a hidden folder leaked: %v", set)
	}

	// Moving the subfolder out of the hidden subtree unhides it again.
	newRoot, err := f.CreateFolder(1, "Visible root", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.MoveFolder(1, child.ID, &newRoot.ID); err != nil {
		t.Fatal(err)
	}
	set, _ = f.HiddenFavorites(1, false)
	if set.Hashes["nnnn"] {
		t.Errorf("favourite stayed hidden after subtree move: %v", set)
	}
}

// Per-user scope: one user's hidden folder never feeds another user's curtain.
func TestHiddenFavorites_PerUserScope(t *testing.T) {
	f := newTestFavorites(t)
	hidden, err := f.CreateFolder(1, "Secret", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add("alice secret", "ahash", "magnet:a", "manual", 1); err != nil {
		t.Fatal(err)
	}
	if err := f.MoveFavoriteToFolder(1, "alice secret", &hidden.ID); err != nil {
		t.Fatal(err)
	}

	other, err := f.HiddenFavorites(2, false)
	if err != nil {
		t.Fatal(err)
	}
	if !other.Empty() {
		t.Errorf("alice's hidden folder leaked into bob's curtain: %v", other)
	}
	all, err := f.HiddenFavorites(0, true)
	if err != nil {
		t.Fatal(err)
	}
	if !all.Hashes["ahash"] {
		t.Errorf("admin curtain (includeAll) should span every user: %v", all)
	}
}

// A nil store returns an empty curtain, never panics (handlers rely on this).
func TestHiddenFavorites_NilStore(t *testing.T) {
	var f *FavoritesStore
	set, err := f.HiddenFavorites(1, false)
	if err != nil || !set.Empty() {
		t.Errorf("nil store: got set=%v err=%v", set, err)
	}
}

// Re-favouriting must not UNLINK the torrent: Add used to overwrite info_hash
// unconditionally, so re-favouriting from a card whose hash resolution failed
// silently removed an (already linked) hidden favourite from the curtain.
func TestAdd_PreservesExistingLinkage(t *testing.T) {
	f := newTestFavorites(t)
	const h1 = "abcdef0123456789abcdef0123456789abcdef012"
	const h2 = "0123456789012345678901234567890123456789"

	if err := f.Add("Movie", h1, "magnet:one", "manual", 1); err != nil {
		t.Fatal(err)
	}
	// Re-favourite with nothing to link → keep the old hash AND magnet.
	if err := f.Add("Movie", "", "", "manual", 1); err != nil {
		t.Fatal(err)
	}
	favs, err := f.List(1, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(favs) != 1 || favs[0].InfoHash != h1 || favs[0].Magnet != "magnet:one" {
		t.Fatalf("empty re-favourite must not unlink: %+v", favs)
	}

	// Re-favourite with a NEW hash → the newer linkage wins.
	if err := f.Add("Movie", h2, "magnet:two", "manual", 1); err != nil {
		t.Fatal(err)
	}
	favs, _ = f.List(1, false, true)
	if len(favs) != 1 || favs[0].InfoHash != h2 || favs[0].Magnet != "magnet:two" {
		t.Fatalf("non-empty re-favourite must relink: %+v", favs)
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
