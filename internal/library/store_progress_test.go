package library

import (
	"context"
	"strings"
	"testing"
)

// Progress-by-hash upsert (issue #201): native clients PUT
// {infoHash, fileIndex, position, duration} without having a library ID.
// The row is keyed by (user_id, info_hash) like every other library write.

func TestUpdateProgressByHash_UpdatesExistingRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seed := func() *Entry {
		e, err := s.Upsert(ctx, UpsertInput{UserID: 1, InfoHash: "h1", Magnet: "magnet:?xt=urn:btih:h1", Name: "Movie", Kind: "video"})
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		return e
	}
	e := seed()

	err := s.UpdateProgressByHash(ctx, 1, ProgressInput{InfoHash: "h1", FileIndex: 3, Position: 900, Duration: 1000})
	if err != nil {
		t.Fatalf("UpdateProgressByHash: %v", err)
	}
	got, err := s.GetByHash(ctx, 1, "h1")
	if err != nil || got == nil {
		t.Fatalf("GetByHash: %v %v", got, err)
	}
	if got.ID != e.ID {
		t.Errorf("row changed identity: %d != %d", got.ID, e.ID)
	}
	if got.ResumeSeconds != 900 || got.DurationSeconds != 1000 {
		t.Errorf("resume and duration wrong: got %v/%v, want 900/1000", got.ResumeSeconds, got.DurationSeconds)
	}
	if got.LastFileIndex != 3 {
		t.Errorf("lastFileIndex=%d, want 3", got.LastFileIndex)
	}
	if !got.Watched {
		t.Errorf("watched=%v, want true at 90 percent", got.Watched)
	}
}

func TestUpdateProgressByHash_InsertsMissingRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpdateProgressByHash(ctx, 2, ProgressInput{InfoHash: "newhash", FileIndex: 0, Position: 60, Duration: 600}); err != nil {
		t.Fatalf("UpdateProgressByHash: %v", err)
	}
	got, err := s.GetByHash(ctx, 2, "newhash")
	if err != nil || got == nil {
		t.Fatalf("GetByHash: %v %v", got, err)
	}
	if got.ResumeSeconds != 60 {
		t.Errorf("resume=%v, want sixty", got.ResumeSeconds)
	}
	if !strings.Contains(got.Magnet, "btih:newhash") {
		t.Errorf("magnet=%q, want btih:newhash prefix", got.Magnet)
	}
	if got.Name == "" {
		t.Error("inserted row must carry a non-empty name")
	}
	if got.Watched {
		t.Errorf("watched=true at 10 percent, want false")
	}
}

func TestUpdateProgressByHash_WatchedThreshold(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	upd := func(position, duration float64) bool {
		if err := s.UpdateProgressByHash(ctx, 3, ProgressInput{InfoHash: "thr", FileIndex: -1, Position: position, Duration: duration}); err != nil {
			t.Fatalf("UpdateProgressByHash: %v", err)
		}
		got, err := s.GetByHash(ctx, 3, "thr")
		if err != nil || got == nil {
			t.Fatalf("GetByHash: %v %v", got, err)
		}
		return got.Watched
	}

	if upd(899, 1000) {
		t.Error("89.9% must not be watched")
	}
	if !upd(900, 1000) {
		t.Error("90% must be watched")
	}
	// Rewatching from the start un-watches.
	if upd(50, 1000) {
		t.Error("5% after watched must clear the flag")
	}
	// Unknown duration never marks watched.
	if upd(999999, 0) {
		t.Error("duration=0 must clear or not set watched")
	}
}

func TestUpdateProgressByHash_DurationAndFileIndexConserved(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Upsert(ctx, UpsertInput{UserID: 4, InfoHash: "keep", Magnet: "magnet:?xt=urn:btih:keep", Name: "Keep", Kind: "video"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.UpdateProgressByHash(ctx, 4, ProgressInput{InfoHash: "keep", FileIndex: 5, Position: 10, Duration: 1000}); err != nil {
		t.Fatalf("first update: %v", err)
	}
	// Second heartbeat without duration/fileIndex knowledge must not clobber.
	if err := s.UpdateProgressByHash(ctx, 4, ProgressInput{InfoHash: "keep", FileIndex: -1, Position: 20, Duration: 0}); err != nil {
		t.Fatalf("second update: %v", err)
	}
	got, err := s.GetByHash(ctx, 4, "keep")
	if err != nil || got == nil {
		t.Fatalf("GetByHash: %v %v", got, err)
	}
	if got.DurationSeconds != 1000 {
		t.Errorf("duration clobbered to %v, want 1000", got.DurationSeconds)
	}
	if got.LastFileIndex != 5 {
		t.Errorf("fileIndex clobbered to %d, want 5", got.LastFileIndex)
	}
}

func TestUpdateProgressByHash_PerUserIsolation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, u := range []int{5, 6} {
		if _, err := s.Upsert(ctx, UpsertInput{UserID: u, InfoHash: "shared", Magnet: "magnet:?xt=urn:btih:shared", Name: "Shared", Kind: "video"}); err != nil {
			t.Fatalf("Upsert u%d: %v", u, err)
		}
	}
	if err := s.UpdateProgressByHash(ctx, 5, ProgressInput{InfoHash: "shared", FileIndex: -1, Position: 950, Duration: 1000}); err != nil {
		t.Fatalf("update: %v", err)
	}
	a, _ := s.GetByHash(ctx, 5, "shared")
	b, _ := s.GetByHash(ctx, 6, "shared")
	if a == nil || b == nil {
		t.Fatalf("rows missing: %v %v", a, b)
	}
	if a.ResumeSeconds != 950 {
		t.Errorf("user5 resume=%v, want 950", a.ResumeSeconds)
	}
	if b.ResumeSeconds != 0 || b.Watched {
		t.Errorf("user6 row leaked: resume=%v watched=%v", b.ResumeSeconds, b.Watched)
	}
}

func TestUpdateProgressByHash_EmptyHashRejected(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpdateProgressByHash(context.Background(), 1, ProgressInput{InfoHash: "  ", FileIndex: -1, Position: 1, Duration: 10}); err == nil {
		t.Fatal("empty infoHash must be rejected")
	}
}
