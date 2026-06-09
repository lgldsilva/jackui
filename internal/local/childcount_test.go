package local

import "testing"

// List populates ChildCount for directories (non-hidden entries) and leaves it
// 0 for files.
func TestList_ChildCount(t *testing.T) {
	_, b := setupTestMount(t)
	entries, err := b.List("test", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]int{}
	for _, e := range entries {
		got[e.Name] = e.ChildCount
	}
	if got["movies"] != 2 { // film.mkv + notes.txt
		t.Errorf("movies ChildCount = %d, want 2", got["movies"])
	}
	if got["music"] != 1 { // song.mp3
		t.Errorf("music ChildCount = %d, want 1", got["music"])
	}
	if got["readme.md"] != 0 { // a file
		t.Errorf("readme.md ChildCount = %d, want 0", got["readme.md"])
	}
}
