package downloads

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "downloads.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	d, err := s.Create(Download{
		UserID:    1,
		InfoHash:  "abc",
		FileIndex: 0,
		FilePath:  "Movie/movie.mkv",
		FileSize:  1000,
		Name:      "Movie",
		Magnet:    "magnet:?xt=urn:btih:abc",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if d.Status != StatusDownloading {
		t.Fatalf("expected status=downloading, got %q", d.Status)
	}
	got, err := s.Get(1, d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Movie" {
		t.Fatalf("name mismatch: %q", got.Name)
	}
}

func TestCreateIdempotent(t *testing.T) {
	s := newTestStore(t)
	base := Download{
		UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:abc",
		Name: "x", FilePath: "x", FileSize: 10,
	}
	d1, err := s.Create(base)
	if err != nil {
		t.Fatalf("Create#1: %v", err)
	}
	d2, err := s.Create(base)
	if err != nil {
		t.Fatalf("Create#2: %v", err)
	}
	if d1.ID != d2.ID {
		t.Fatalf("expected same ID on re-create, got %d vs %d", d1.ID, d2.ID)
	}
}

func TestPauseResumeFlowsThroughCreate(t *testing.T) {
	s := newTestStore(t)
	base := Download{
		UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:abc",
		Name: "x", FilePath: "x", FileSize: 10,
	}
	d, err := s.Create(base)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetStatus(d.ID, StatusPaused); err != nil {
		t.Fatalf("SetStatus pause: %v", err)
	}
	// Re-create should resume the paused entry, not error out.
	d2, err := s.Create(base)
	if err != nil {
		t.Fatalf("Create resume: %v", err)
	}
	if d2.Status != StatusDownloading {
		t.Fatalf("expected resumed entry to be downloading, got %q", d2.Status)
	}
}

func TestUpdateProgressAndComplete(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{
		UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "m", Name: "x", FileSize: 1000,
	})
	if err := s.UpdateProgress(d.ID, 500); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.BytesDownloaded != 500 || got.Progress != 0.5 {
		t.Fatalf("progress not tracked: %+v", got)
	}
	if err := s.SetStatus(d.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}
	got, _ = s.Get(1, d.ID)
	if got.Status != StatusCompleted || got.CompletedAt == nil {
		t.Fatalf("expected completed with timestamp, got %+v", got)
	}
}

func TestListActiveOnlyDownloading(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "a"})
	b, _ := s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "b"})
	c, _ := s.Create(Download{UserID: 1, InfoHash: "c", FileIndex: 0, Magnet: "m", Name: "c"})
	_ = s.SetStatus(b.ID, StatusPaused)
	_ = s.SetStatus(c.ID, StatusCompleted)
	active, err := s.ListActive()
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 1 || active[0].ID != a.ID {
		t.Fatalf("expected only #%d active, got %+v", a.ID, active)
	}
}

func TestDeleteOwnership(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "x"})
	// Wrong user — should fail
	if err := s.Delete(99, d.ID); err == nil {
		t.Fatal("expected Delete to fail with wrong user")
	}
	if err := s.Delete(1, d.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(1, d.ID); err == nil {
		t.Fatal("expected Get after Delete to fail")
	}
}
