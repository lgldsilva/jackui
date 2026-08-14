package downloads

import (
	"testing"
	"time"
)

func TestStopSeedMarksRowAndResumeSeedClears(t *testing.T) {
	s := newTestStore(t)
	d, err := s.Create(Download{
		UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:abc",
		Name: "x", FilePath: "x", FileSize: 10,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetStatus(1, d.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}

	if err := s.StopSeed(1, d.ID); err != nil {
		t.Fatalf("StopSeed: %v", err)
	}
	got, err := s.Get(1, d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SeedStoppedAt == nil {
		t.Fatal("expected SeedStoppedAt to be set")
	}
	if time.Since(*got.SeedStoppedAt) > time.Minute {
		t.Fatalf("SeedStoppedAt too old: %v", got.SeedStoppedAt)
	}

	if err := s.ResumeSeed(1, d.ID); err != nil {
		t.Fatalf("ResumeSeed: %v", err)
	}
	got, err = s.Get(1, d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SeedStoppedAt != nil {
		t.Fatalf("expected SeedStoppedAt to be cleared, got %v", got.SeedStoppedAt)
	}
}

func TestStopSeedByInfoHashOnlyCompletedRows(t *testing.T) {
	s := newTestStore(t)
	completed, err := s.Create(Download{
		UserID: 1, InfoHash: "shared", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:shared",
		Name: "c", FilePath: "c", FileSize: 10,
	})
	if err != nil {
		t.Fatalf("Create completed: %v", err)
	}
	if err := s.SetStatus(1, completed.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}

	paused, err := s.Create(Download{
		UserID: 1, InfoHash: "shared", FileIndex: 1, Magnet: "magnet:?xt=urn:btih:shared",
		Name: "p", FilePath: "p", FileSize: 10,
	})
	if err != nil {
		t.Fatalf("Create paused: %v", err)
	}
	if err := s.SetStatus(1, paused.ID, StatusPaused); err != nil {
		t.Fatalf("SetStatus paused: %v", err)
	}

	if err := s.StopSeedByInfoHash(1, "shared"); err != nil {
		t.Fatalf("StopSeedByInfoHash: %v", err)
	}

	gotCompleted, _ := s.Get(1, completed.ID)
	if gotCompleted.SeedStoppedAt == nil {
		t.Fatal("completed row should be seed-stopped")
	}
	gotPaused, _ := s.Get(1, paused.ID)
	if gotPaused.SeedStoppedAt != nil {
		t.Fatal("paused row should NOT be seed-stopped")
	}
}

func TestSetStatusCompletedClearsSeedStopped(t *testing.T) {
	s := newTestStore(t)
	d, err := s.Create(Download{
		UserID: 1, InfoHash: "abc", FileIndex: 0, Magnet: "magnet:?xt=urn:btih:abc",
		Name: "x", FilePath: "x", FileSize: 10,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetStatus(1, d.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}
	if err := s.StopSeed(1, d.ID); err != nil {
		t.Fatalf("StopSeed: %v", err)
	}

	// Move to paused then back to completed: auto-seed should be re-enabled.
	if err := s.SetStatus(1, d.ID, StatusPaused); err != nil {
		t.Fatalf("SetStatus paused: %v", err)
	}
	if err := s.SetStatus(1, d.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed again: %v", err)
	}

	got, err := s.Get(1, d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SeedStoppedAt != nil {
		t.Fatal("re-completing should clear SeedStoppedAt")
	}
}
