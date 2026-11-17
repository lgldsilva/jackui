package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

// countingScan returns a listScan whose directory reads are counted.
func countingScan(ctx context.Context, reads *int) *listScan {
	return newListScan(ctx, func(dir string) ([]os.DirEntry, error) {
		*reads++
		return os.ReadDir(dir)
	})
}

func incompleteByName(entries []Entry) map[string]bool {
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name] = e.Incomplete
	}
	return got
}

// A finished library has no ".part", so nothing short-circuits: the listing
// must stop reading after its per-entry budget instead of walking the tree.
func TestList_PartScanBoundedPerEntry(t *testing.T) {
	root := t.TempDir()
	const nested = 200
	for i := 0; i < nested; i++ {
		mustMkdir(t, filepath.Join(root, "Library", fmt.Sprintf("Show %03d", i), "Season 1"))
	}
	b := NewBrowser([]config.ExternalMount{{Name: "test", Path: root}})

	reads := 0
	entries, err := b.list(countingScan(context.Background(), &reads), "test", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 || entries[0].ChildCount != nested || entries[0].Incomplete {
		t.Fatalf("entries = %+v, want one complete dir with %d children", entries, nested)
	}
	// listed dir + the entry itself + the per-entry nested budget
	if want := 2 + partScanDirsPerEntry; reads > want {
		t.Fatalf("directory reads = %d, want <= %d (tree holds %d dirs)", reads, want, 1+2*nested)
	}
}

// A wide listing is capped as a whole, and every entry still gets its own
// read (child count + direct ".part") after the shared budget runs out.
func TestList_PartScanBoundedPerListing(t *testing.T) {
	root := t.TempDir()
	const folders, subdirs = 60, 20
	for i := 0; i < folders; i++ {
		for j := 0; j < subdirs; j++ {
			mustMkdir(t, filepath.Join(root, fmt.Sprintf("Folder %02d", i), fmt.Sprintf("Sub %02d", j)))
		}
	}
	// Last in read order, so the shared budget is already spent when it is reached.
	mustMkdir(t, filepath.Join(root, "zz Active"))
	mustWrite(t, filepath.Join(root, "zz Active", "movie.mkv.part"), "partial")
	b := NewBrowser([]config.ExternalMount{{Name: "test", Path: root}})

	reads := 0
	entries, err := b.list(countingScan(context.Background(), &reads), "test", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if want := 1 + (folders + 1) + partScanDirsPerListing; reads > want {
		t.Fatalf("directory reads = %d, want <= %d (tree holds %d dirs)", reads, want, 2+folders*(1+subdirs))
	}
	got := incompleteByName(entries)
	if !got["zz Active"] {
		t.Error("a direct .part must be flagged even after the listing budget is spent")
	}
	if got["Folder 00"] {
		t.Error("a folder without .part must not be flagged")
	}
	for _, e := range entries {
		if e.Name != "zz Active" && e.ChildCount != subdirs {
			t.Fatalf("ChildCount[%q] = %d, want %d", e.Name, e.ChildCount, subdirs)
		}
	}
}

// Nested ".part" files are still found within the budget (breadth-first), in
// hidden directories too; beyond the budget the folder is not flagged.
func TestList_PartScanNestedSemantics(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "Deep", "a", "b", "c"))
	mustWrite(t, filepath.Join(root, "Deep", "a", "b", "c", "e.mkv.part"), "partial")
	mustMkdir(t, filepath.Join(root, "Hidden", ".pad"))
	mustWrite(t, filepath.Join(root, "Hidden", ".pad", "x.part"), "partial")
	// The .part sits in the last of more subdirectories than one entry may read.
	for i := 0; i <= partScanDirsPerEntry; i++ {
		mustMkdir(t, filepath.Join(root, "Wide", fmt.Sprintf("S%03d", i)))
	}
	mustWrite(t, filepath.Join(root, "Wide", fmt.Sprintf("S%03d", partScanDirsPerEntry), "e.mkv.part"), "partial")
	b := NewBrowser([]config.ExternalMount{{Name: "test", Path: root}})

	entries, err := b.List("test", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := incompleteByName(entries)
	for name, want := range map[string]bool{"Deep": true, "Hidden": true, "Wide": false} {
		if got[name] != want {
			t.Errorf("Incomplete[%q] = %v, want %v", name, got[name], want)
		}
	}
}

// Unreadable directories never fail the listing: the entry itself reports
// (0, false) and an unreadable nested directory is skipped.
func TestListScan_ReadErrors(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "bad"))
	mustMkdir(t, filepath.Join(root, "good"))
	mustWrite(t, filepath.Join(root, "good", "e.mkv.part"), "partial")
	failing := func(dir string) ([]os.DirEntry, error) {
		if filepath.Base(dir) == "bad" {
			return nil, errors.New("boom")
		}
		return os.ReadDir(dir)
	}

	scan := newListScan(context.Background(), failing)
	if n, inc := scan.inspectDir(filepath.Join(root, "bad")); n != 0 || inc {
		t.Errorf("inspectDir(unreadable) = (%d, %v), want (0, false)", n, inc)
	}
	if n, inc := scan.inspectDir(root); n != 2 || !inc {
		t.Errorf("inspectDir(root) = (%d, %v), want (2, true): unreadable sibling skipped", n, inc)
	}

	b := NewBrowser([]config.ExternalMount{{Name: "test", Path: root}})
	if _, err := b.list(newListScan(context.Background(), func(string) ([]os.DirEntry, error) {
		return nil, errors.New("boom")
	}), "test", ""); err == nil {
		t.Error("list must surface a read error on the listed directory itself")
	}
}

// A cancelled request stops the listing instead of reading on.
func TestListContext_Cancelled(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "A", "nested"))
	mustMkdir(t, filepath.Join(root, "B", "nested"))
	b := NewBrowser([]config.ExternalMount{{Name: "test", Path: root}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.ListContext(ctx, "test", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListContext(cancelled) err = %v, want context.Canceled", err)
	}

	// Cancelled mid-scan: the nested walk stops without spending budget.
	reads := 0
	scan := countingScan(ctx, &reads)
	if _, inc := scan.inspectDir(filepath.Join(root, "A")); inc || reads != 1 {
		t.Errorf("inspectDir(cancelled) incomplete=%v reads=%d, want false and 1", inc, reads)
	}
}
