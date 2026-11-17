package downloads

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

// seedFileCompleted creates a completed per-file row owned by userID.
func seedFileCompleted(t *testing.T, s *Store, userID int, hash string, idx int, path string) {
	t.Helper()
	d, err := s.Create(Download{
		UserID: userID, InfoHash: hash, FileIndex: idx,
		Magnet: "magnet:?xt=urn:btih:" + hash, Name: "Pack", FilePath: path,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetStatus(userID, d.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
}

// The batch lookup is a pure optimisation: for every index it must answer
// exactly what the per-file GetCompletedPathRel answers — per-file rows as-is,
// files inside the whole-torrent tree, and "" (omitted) for traversal, missing
// files, directories and empty rel paths.
func TestGetCompletedPathsRel_MatchesPerFileLookup(t *testing.T) {
	s := dlwNewStore(t)
	destDir := t.TempDir()
	mustWrite(t, destDir, "Sub", "a.mkv")
	mustWrite(t, destDir, "b.mkv")
	if err := os.MkdirAll(filepath.Join(destDir, "Dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Dir(destDir), "secret.txt")
	seedWholeCompleted(t, s, "lay1", "Pack", destDir)
	// A per-file row for index 1 wins over the whole-torrent tree.
	seedFileCompleted(t, s, 2, "lay1", 1, "/dl/renamed/b.mkv")

	relPaths := []string{
		"Pack/Sub/a.mkv",     // in the moved tree
		"Pack/b.mkv",         // per-file row wins
		"Pack/ghost.mkv",     // missing on disk
		"Pack/../secret.txt", // traversal
		"Pack/Dir",           // a directory
		"",                   // no metainfo
	}
	for _, userID := range []int{-1, 1, 2, 3} {
		got, err := s.GetCompletedPathsRel("lay1", relPaths, userID)
		if err != nil {
			t.Fatalf("user %d: %v", userID, err)
		}
		for i, rel := range relPaths {
			want, err := s.GetCompletedPathRel("lay1", i, rel, userID)
			if err != nil {
				t.Fatalf("user %d idx %d: %v", userID, i, err)
			}
			if p, ok := got[i]; p != want || ok != (want != "") {
				t.Errorf("user %d idx %d (%q): batch = (%q, %v), per-file = %q", userID, i, rel, p, ok, want)
			}
		}
	}
	// Sanity on the fixture, so the equivalence above is not vacuous.
	got, _ := s.GetCompletedPathsRel("lay1", relPaths, -1)
	if len(got) != 2 || got[0] != filepath.Join(destDir, "Sub", "a.mkv") || got[1] != "/dl/renamed/b.mkv" {
		t.Fatalf("any-owner layout = %v", got)
	}
}

func TestGetCompletedPathsRel_ManyPerFileRows(t *testing.T) {
	s := dlwNewStore(t)
	const n = 50
	relPaths := make([]string, n)
	for i := 0; i < n; i++ {
		relPaths[i] = fmt.Sprintf("Pack/f%02d.bin", i)
		// Odd files only: a partial selection leaves the rest unresolved.
		if i%2 == 1 {
			seedFileCompleted(t, s, 1, "lay2", i, fmt.Sprintf("/dl/Pack/f%02d.bin", i))
		}
	}
	got, err := s.GetCompletedPathsRel("lay2", relPaths, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n/2 {
		t.Fatalf("resolved %d files, want %d", len(got), n/2)
	}
	for i := 1; i < n; i += 2 {
		if want := fmt.Sprintf("/dl/Pack/f%02d.bin", i); got[i] != want {
			t.Fatalf("idx %d = %q, want %q", i, got[i], want)
		}
	}
}

func TestGetCompletedPathsRel_GuardsAndErrors(t *testing.T) {
	var nilS *Store
	if got, err := nilS.GetCompletedPathsRel("h", []string{"a"}, -1); err != nil || len(got) != 0 {
		t.Fatalf("nil store: got (%v, %v), want empty", got, err)
	}

	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1)
	s, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetCompletedPathsRel("h", nil, -1); err != nil || len(got) != 0 {
		t.Fatalf("no files: got (%v, %v), want empty", got, err)
	}
	if got, err := s.GetCompletedPathsRel("unknown", []string{"Pack/a.mkv"}, -1); err != nil || len(got) != 0 {
		t.Fatalf("unknown hash: got (%v, %v), want empty", got, err)
	}
	// A failing query surfaces as an error, never as a silently empty layout.
	_ = pool.Close()
	if _, err := s.GetCompletedPathsRel("h", []string{"Pack/a.mkv"}, -1); err == nil {
		t.Fatal("closed pool: want an error")
	}
	if _, err := s.GetCompletedPathsRel("h", []string{"Pack/a.mkv"}, 1); err == nil {
		t.Fatal("closed pool (user-scoped): want an error")
	}
}
