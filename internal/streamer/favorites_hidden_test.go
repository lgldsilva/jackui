package streamer

import "testing"

// A hidden folder stays out of the default listing (sidebar) but shows when the
// caller opts in (the UI's easter egg). SetFolderHidden toggles it.
func TestFavoritesHiddenFolderListing(t *testing.T) {
	f := newTestFavorites(t)
	normal, err := f.CreateFolder(1, "Normal", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := f.CreateFolder(1, "Hidden", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !hidden.Hidden {
		t.Fatal("CreateFolder(hidden=true) should set Hidden")
	}

	// Default listing hides it.
	vis, err := f.ListFolders(1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(vis) != 1 || vis[0].ID != normal.ID {
		t.Fatalf("default listing should show only the normal folder, got %d", len(vis))
	}
	// Opt-in reveals both.
	all, err := f.ListFolders(1, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("includeHidden should show both folders, got %d", len(all))
	}

	// Un-hiding brings it back into the default listing.
	if err := f.SetFolderHidden(1, hidden.ID, false); err != nil {
		t.Fatal(err)
	}
	vis, _ = f.ListFolders(1, false)
	if len(vis) != 2 {
		t.Fatalf("after un-hide, default listing should show both, got %d", len(vis))
	}
}

// Favourites inside a hidden folder must not leak into the default List (the
// "all" view) — only when includeHidden is set.
func TestFavoritesHiddenFolderHidesItems(t *testing.T) {
	f := newTestFavorites(t)
	hidden, err := f.CreateFolder(1, "Secret", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add("secret movie", "hsecret", "magnet:s", "manual", 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Add("public movie", "hpub", "magnet:p", "manual", 1); err != nil {
		t.Fatal(err)
	}
	if err := f.MoveFavoriteToFolder(1, "secret movie", &hidden.ID); err != nil {
		t.Fatal(err)
	}

	// Default list: the secret one is filtered out.
	def, err := f.List(1, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, fav := range def {
		if fav.Name == "secret movie" {
			t.Fatal("favourite in a hidden folder leaked into the default list")
		}
	}
	if len(def) != 1 {
		t.Fatalf("default list should have 1 (public), got %d", len(def))
	}

	// includeHidden: both show.
	full, err := f.List(1, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 2 {
		t.Fatalf("includeHidden list should have both, got %d", len(full))
	}
}
