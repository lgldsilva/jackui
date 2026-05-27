package library

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lib.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertCreatesAndUpdates(t *testing.T) {
	s := newTestStore(t)

	e, err := s.Upsert(1, "abc123", "magnet:?xt=urn:btih:abc123", "Test Movie", 0, 1024, "video")
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if e.ID == 0 {
		t.Fatal("expected positive ID")
	}
	if e.Name != "Test Movie" {
		t.Errorf("name: got %q", e.Name)
	}

	// Re-upsert with new name — should update, not create new row
	e2, err := s.Upsert(1, "abc123", "magnet:?xt=urn:btih:abc123", "Renamed", 1, 2048, "video")
	if err != nil {
		t.Fatalf("Upsert update: %v", err)
	}
	if e2.ID != e.ID {
		t.Fatalf("expected same ID (upsert), got %d vs %d", e2.ID, e.ID)
	}
	if e2.Name != "Renamed" || e2.PrimaryFileIndex != 1 {
		t.Errorf("update didn't persist: name=%q file=%d", e2.Name, e2.PrimaryFileIndex)
	}
}

func TestPerUserIsolation(t *testing.T) {
	s := newTestStore(t)

	s.Upsert(1, "ha", "magnet:?xt=urn:btih:ha", "A's movie", 0, 0, "video")
	s.Upsert(2, "hb", "magnet:?xt=urn:btih:hb", "B's movie", 0, 0, "video")

	listA, _ := s.List(1, false, 0)
	if len(listA) != 1 || listA[0].Name != "A's movie" {
		t.Fatalf("user A: expected only own row, got %v", listA)
	}

	listB, _ := s.List(2, false, 0)
	if len(listB) != 1 || listB[0].Name != "B's movie" {
		t.Fatalf("user B: expected only own row, got %v", listB)
	}

	listAll, _ := s.List(0, true, 0)
	if len(listAll) != 2 {
		t.Fatalf("admin (includeAll): expected 2, got %d", len(listAll))
	}
}

func TestUpdateResume(t *testing.T) {
	s := newTestStore(t)
	e, _ := s.Upsert(1, "ha", "magnet:x", "Movie", 0, 0, "video")

	// Fresh entry: last file untracked (-1).
	if e.LastFileIndex != -1 {
		t.Errorf("fresh LastFileIndex: got %d want -1", e.LastFileIndex)
	}

	if err := s.UpdateResume(e.ID, 1, 123.5, 1800, 3, false); err != nil {
		t.Fatalf("UpdateResume: %v", err)
	}
	got, _ := s.GetByID(e.ID, 1, false)
	if got.ResumeSeconds != 123.5 {
		t.Errorf("resume: got %v want 123.5", got.ResumeSeconds)
	}
	if got.DurationSeconds != 1800 {
		t.Errorf("duration: got %v want 1800", got.DurationSeconds)
	}
	if got.LastFileIndex != 3 {
		t.Errorf("lastFileIndex: got %d want 3", got.LastFileIndex)
	}

	// Updating with duration=0 should NOT clobber existing duration; fileIndex=-1
	// must leave the tracked file untouched.
	s.UpdateResume(e.ID, 1, 200, 0, -1, false)
	got2, _ := s.GetByID(e.ID, 1, false)
	if got2.DurationSeconds != 1800 {
		t.Errorf("duration overwritten: %v", got2.DurationSeconds)
	}
	if got2.LastFileIndex != 3 {
		t.Errorf("lastFileIndex clobbered by -1: got %d want 3", got2.LastFileIndex)
	}
}

func TestDeleteRefusesOtherUser(t *testing.T) {
	s := newTestStore(t)
	e, _ := s.Upsert(1, "ha", "magnet:x", "Movie", 0, 0, "")

	// User 2 tries to delete user 1's entry — should fail
	if err := s.Delete(e.ID, 2, false); err == nil {
		t.Fatal("expected error when deleting another user's entry")
	}

	// Admin (includeAll=true) succeeds
	if err := s.Delete(e.ID, 0, true); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
	got, _ := s.GetByID(e.ID, 0, true)
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestUpsertRequiresHashAndMagnet(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Upsert(1, "", "magnet:x", "X", 0, 0, ""); err == nil {
		t.Error("expected error for empty hash")
	}
	if _, err := s.Upsert(1, "h", "", "X", 0, 0, ""); err == nil {
		t.Error("expected error for empty magnet")
	}
}
