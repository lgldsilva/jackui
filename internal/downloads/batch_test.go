package downloads

import "testing"

// BatchCreate inserts every file of one torrent in a single transaction and
// reports created vs requeued. A fresh batch is all-created.
func TestBatchCreate_AllNew(t *testing.T) {
	s := newTestStore(t)
	rows := []Download{
		{UserID: 1, InfoHash: "abc", FileIndex: 0, FilePath: "S01/E01.mkv", FileSize: 100, Name: "Show", Magnet: "magnet:?xt=urn:btih:abc"},
		{UserID: 1, InfoHash: "abc", FileIndex: 1, FilePath: "S01/E02.mkv", FileSize: 200, Name: "Show", Magnet: "magnet:?xt=urn:btih:abc"},
		{UserID: 1, InfoHash: "abc", FileIndex: 2, FilePath: "S01/E03.mkv", FileSize: 300, Name: "Show", Magnet: "magnet:?xt=urn:btih:abc"},
	}
	res, err := s.BatchCreate(rows)
	if err != nil {
		t.Fatalf("BatchCreate: %v", err)
	}
	if len(res.Rows) != 3 || res.Created != 3 || res.Requeued != 0 {
		t.Fatalf("got rows=%d created=%d requeued=%d, want 3/3/0", len(res.Rows), res.Created, res.Requeued)
	}
	// Rows come back in input order with their per-file fields.
	for i, r := range res.Rows {
		if r.FileIndex != i {
			t.Fatalf("row[%d].FileIndex = %d, want %d", i, r.FileIndex, i)
		}
		if r.Status != StatusQueued {
			t.Fatalf("row[%d].Status = %q, want queued", i, r.Status)
		}
	}
	// All three live in the DB.
	all, err := s.List(1)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List returned %d rows, want 3", len(all))
	}
}

// BatchCreate reuses Create's idempotency: an already-present file isn't
// duplicated and is counted as requeued, not created.
func TestBatchCreate_Idempotent(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(Download{UserID: 1, InfoHash: "abc", FileIndex: 0, FilePath: "E01.mkv", FileSize: 100, Name: "Show", Magnet: "magnet:?xt=urn:btih:abc"}); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	res, err := s.BatchCreate([]Download{
		{UserID: 1, InfoHash: "abc", FileIndex: 0, FilePath: "E01.mkv", FileSize: 100, Name: "Show", Magnet: "magnet:?xt=urn:btih:abc"},
		{UserID: 1, InfoHash: "abc", FileIndex: 1, FilePath: "E02.mkv", FileSize: 200, Name: "Show", Magnet: "magnet:?xt=urn:btih:abc"},
	})
	if err != nil {
		t.Fatalf("BatchCreate: %v", err)
	}
	if res.Created != 1 || res.Requeued != 1 {
		t.Fatalf("created=%d requeued=%d, want 1/1", res.Created, res.Requeued)
	}
	all, _ := s.List(1)
	if len(all) != 2 {
		t.Fatalf("List returned %d rows, want 2 (no dup)", len(all))
	}
}

// A paused/failed file in the batch is sent back to the queue (re-queue path of
// the shared createOne).
func TestBatchCreate_RequeuesPaused(t *testing.T) {
	s := newTestStore(t)
	seed, err := s.Create(Download{UserID: 1, InfoHash: "abc", FileIndex: 0, FilePath: "E01.mkv", FileSize: 100, Name: "Show", Magnet: "magnet:?xt=urn:btih:abc"})
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	if err := s.SetStatus(1, seed.ID, StatusPaused); err != nil {
		t.Fatalf("SetStatus paused: %v", err)
	}
	res, err := s.BatchCreate([]Download{
		{UserID: 1, InfoHash: "abc", FileIndex: 0, FilePath: "E01.mkv", FileSize: 100, Name: "Show", Magnet: "magnet:?xt=urn:btih:abc"},
	})
	if err != nil {
		t.Fatalf("BatchCreate: %v", err)
	}
	if res.Created != 0 || res.Requeued != 1 {
		t.Fatalf("created=%d requeued=%d, want 0/1", res.Created, res.Requeued)
	}
	got, _ := s.Get(1, seed.ID)
	if got.Status != StatusQueued {
		t.Fatalf("paused row not re-queued: status=%q", got.Status)
	}
}

// A malformed row aborts the WHOLE transaction (all-or-nothing): nothing the
// batch would have inserted survives.
func TestBatchCreate_AtomicRollback(t *testing.T) {
	s := newTestStore(t)
	res, err := s.BatchCreate([]Download{
		{UserID: 1, InfoHash: "abc", FileIndex: 0, FilePath: "E01.mkv", FileSize: 100, Name: "Show", Magnet: "magnet:?xt=urn:btih:abc"},
		// Missing magnet → createOne returns an error mid-batch.
		{UserID: 1, InfoHash: "abc", FileIndex: 1, FilePath: "E02.mkv", FileSize: 200, Name: "Show", Magnet: ""},
	})
	if err == nil {
		t.Fatalf("expected error, got res=%+v", res)
	}
	all, _ := s.List(1)
	if len(all) != 0 {
		t.Fatalf("rollback failed: %d rows persisted, want 0", len(all))
	}
}

func TestBatchCreate_Empty(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.BatchCreate(nil); err == nil {
		t.Fatal("expected error for empty batch")
	}
}
