package local

import (
	"path/filepath"
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

// List flags entries still downloading: a ".part" file, or a directory whose
// tree holds one. Completed files/dirs are not flagged.
func TestList_IncompleteIndicator(t *testing.T) {
	root := t.TempDir()
	// downloading single-file torrent: folder with a .part inside
	mustMkdir(t, filepath.Join(root, "Movie.mkv"))
	mustWrite(t, filepath.Join(root, "Movie.mkv", "Movie.mkv.part"), "partial")
	// downloading multi-file torrent: .part nested one level down
	mustMkdir(t, filepath.Join(root, "Pack", "S01"))
	mustWrite(t, filepath.Join(root, "Pack", "S01", "E01.mkv.part"), "partial")
	// completed folder + a loose completed file + a loose .part file
	mustMkdir(t, filepath.Join(root, "Done"))
	mustWrite(t, filepath.Join(root, "Done", "ready.mkv"), "ok")
	mustWrite(t, filepath.Join(root, "loose.mkv.part"), "partial")

	b := NewBrowser([]config.ExternalMount{{Name: "test", Path: root}})
	entries, err := b.List("test", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name] = e.Incomplete
	}
	for name, want := range map[string]bool{
		"Movie.mkv":      true,  // dir with direct .part
		"Pack":           true,  // dir with nested .part
		"Done":           false, // fully downloaded dir
		"loose.mkv.part": true,  // a bare .part file
	} {
		if got[name] != want {
			t.Errorf("Incomplete[%q] = %v, want %v", name, got[name], want)
		}
	}
}

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
