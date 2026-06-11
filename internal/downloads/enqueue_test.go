package downloads

import (
	"path/filepath"
	"testing"
)

func newEnqStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "dl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestEnqueueMagnet(t *testing.T) {
	s := newEnqStore(t)
	if err := s.EnqueueMagnet(3, "ABCDEF1234", "Show.S01E02.1080p", "magnet:?xt=urn:btih:abcdef1234", "trk"); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(3)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v %v", list, err)
	}
	d := list[0]
	if d.InfoHash != "abcdef1234" {
		t.Fatalf("hash must be lower-cased, got %q", d.InfoHash)
	}
	if d.FileIndex != -1 {
		t.Fatalf("FileIndex must be -1 (best file), got %d", d.FileIndex)
	}
	if d.Status != StatusQueued || d.Name != "Show.S01E02.1080p" || d.Tracker != "trk" {
		t.Fatalf("row mismatch: %+v", d)
	}
	// Idempotent: a second enqueue of the same hash adds nothing.
	if err := s.EnqueueMagnet(3, "abcdef1234", "Show.S01E02.1080p", "magnet:?xt=urn:btih:abcdef1234", "trk"); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List(3)
	if len(list) != 1 {
		t.Fatalf("enqueue must be idempotent, got %d rows", len(list))
	}
}

func TestEnqueueMagnet_RequiresHashAndMagnet(t *testing.T) {
	s := newEnqStore(t)
	if err := s.EnqueueMagnet(1, "", "name", "magnet:x", ""); err == nil {
		t.Fatal("expected error without infoHash")
	}
	if err := s.EnqueueMagnet(1, "hash", "name", "", ""); err == nil {
		t.Fatal("expected error without magnet")
	}
}
