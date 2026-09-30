package local

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestMigrateToUserSubpath(t *testing.T) {
	root := t.TempDir()
	// Root layout before migration:
	//   alice.mkv      → attributed to "alice"
	//   orphan.mkv     → no owner → fallback "admin"
	//   bob/           → already a known user's subdir (left alone)
	//   bob/keep.mkv
	mustWrite(t, filepath.Join(root, "alice.mkv"), "A")
	mustWrite(t, filepath.Join(root, "orphan.mkv"), "O")
	mustMkdir(t, filepath.Join(root, "bob"))
	mustWrite(t, filepath.Join(root, "bob", "keep.mkv"), "K")

	b := NewBrowser([]config.ExternalMount{
		{Name: "Meus downloads", Path: root, UserSubpath: true},
	})

	known := map[string]bool{"alice": true, "bob": true, "admin": true}
	attribute := func(abs string) (string, bool) {
		if filepath.Base(abs) == "alice.mkv" {
			return "alice", true
		}
		return "", false // orphan.mkv is unattributable
	}

	res, err := b.MigrateToUserSubpath("Meus downloads", known, "admin", attribute)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if len(res.Moved) != 2 {
		t.Fatalf("moved %d entries, want 2: %+v", len(res.Moved), res.Moved)
	}
	if res.Skipped != 1 {
		t.Errorf("skipped=%d, want 1 (the bob/ subdir)", res.Skipped)
	}

	// alice.mkv → alice/alice.mkv
	if !fileExists(filepath.Join(root, "alice", "alice.mkv")) {
		t.Error("alice.mkv was not moved to alice/")
	}
	// orphan.mkv → admin/orphan.mkv (fallback)
	if !fileExists(filepath.Join(root, "admin", "orphan.mkv")) {
		t.Error("orphan.mkv did not go to the admin/ fallback")
	}
	// bob/ untouched
	if !fileExists(filepath.Join(root, "bob", "keep.mkv")) {
		t.Error("bob/keep.mkv was touched unexpectedly")
	}
	// originals gone from root
	if fileExists(filepath.Join(root, "alice.mkv")) || fileExists(filepath.Join(root, "orphan.mkv")) {
		t.Error("original files still exist in the root")
	}

	// fallback flag is set only for the orphan
	for _, m := range res.Moved {
		if m.Name == "orphan.mkv" && !m.Fallback {
			t.Error("orphan.mkv should be flagged as fallback")
		}
		if m.Name == "alice.mkv" && m.Fallback {
			t.Error("alice.mkv should not be fallback")
		}
	}

	// Idempotency: a second run moves nothing (everything is now scoped).
	res2, err := b.MigrateToUserSubpath("Meus downloads", known, "admin", attribute)
	if err != nil {
		t.Fatalf("migrate (2nd): %v", err)
	}
	if len(res2.Moved) != 0 {
		t.Errorf("2nd run moved %d, want 0 (idempotency): %+v", len(res2.Moved), res2.Moved)
	}
}

func TestMigrateToUserSubpath_SharedMountIsNoop(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "x.mkv"), "X")

	// UserSubpath false → migration must do nothing.
	b := NewBrowser([]config.ExternalMount{{Name: "Shared", Path: root}})
	res, err := b.MigrateToUserSubpath("Shared", map[string]bool{"admin": true}, "admin", func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(res.Moved) != 0 {
		t.Errorf("shared mount moved %d, want 0", len(res.Moved))
	}
	if !fileExists(filepath.Join(root, "x.mkv")) {
		t.Error("file should not have been moved on a shared mount")
	}
}

func TestMigrateToUserSubpath_Collision(t *testing.T) {
	root := t.TempDir()
	// A loose entry collides with one already in the owner's subdir.
	mustMkdir(t, filepath.Join(root, "alice"))
	mustWrite(t, filepath.Join(root, "alice", "movie.mkv"), "existing")
	mustWrite(t, filepath.Join(root, "movie.mkv"), "incoming")

	b := NewBrowser([]config.ExternalMount{{Name: "M", Path: root, UserSubpath: true}})
	known := map[string]bool{"alice": true}
	attribute := func(string) (string, bool) { return "alice", true }

	res, err := b.MigrateToUserSubpath("M", known, "alice", attribute)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// New behaviour: never mint "movie (1).mkv" (that orphaned in-flight downloads).
	// The incoming entry stays at the root, counted as a conflict for manual merge.
	if len(res.Moved) != 0 {
		t.Fatalf("moved %d, want 0 (collision must not relocate)", len(res.Moved))
	}
	if res.Conflicts != 1 {
		t.Errorf("conflicts=%d, want 1", res.Conflicts)
	}
	// Existing file preserved, incoming left at root, NO numbered duplicate.
	if existing, _ := os.ReadFile(filepath.Join(root, "alice", "movie.mkv")); string(existing) != "existing" {
		t.Errorf("existing file was overwritten: %q", existing)
	}
	if incoming, _ := os.ReadFile(filepath.Join(root, "movie.mkv")); string(incoming) != "incoming" {
		t.Errorf("root entry should have remained intact: %q", incoming)
	}
	if fileExists(filepath.Join(root, "alice", "movie (1).mkv")) {
		t.Error("should not have created numbered duplicate 'movie (1).mkv'")
	}
}

// A download still in progress (anacrolix .part files) must never be relocated —
// moving it out from under the live torrent strands its pieces and re-downloads.
func TestMigrateToUserSubpath_SkipsActiveDownload(t *testing.T) {
	root := t.TempDir()
	// A loose torrent folder mid-download: contains a .part file.
	mustMkdir(t, filepath.Join(root, "Morgpie"))
	mustWrite(t, filepath.Join(root, "Morgpie", "clip.mp4.part"), "partial")
	// A completed loose file alongside it (should still migrate normally).
	mustWrite(t, filepath.Join(root, "done.mkv"), "D")

	b := NewBrowser([]config.ExternalMount{{Name: "M", Path: root, UserSubpath: true}})
	known := map[string]bool{"admin": true}
	attribute := func(string) (string, bool) { return "admin", true }

	res, err := b.MigrateToUserSubpath("M", known, "admin", attribute)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res.Active != 1 {
		t.Errorf("active=%d, want 1 (the .part download)", res.Active)
	}
	// In-progress folder untouched at the root.
	if !fileExists(filepath.Join(root, "Morgpie", "clip.mp4.part")) {
		t.Error("active download (.part) should not have been moved")
	}
	if fileExists(filepath.Join(root, "admin", "Morgpie")) {
		t.Error("active download was unexpectedly relocated to admin/")
	}
	// The completed file still migrates.
	if !fileExists(filepath.Join(root, "admin", "done.mkv")) {
		t.Error("completed file should have migrated to admin/")
	}
}
