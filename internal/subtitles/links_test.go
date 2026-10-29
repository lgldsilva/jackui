package subtitles

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

// newLinksStore returns a store plus the raw pool handle (for row-count
// assertions and error-path simulation) over the process-wide migrated test
// schema.
func newLinksStore(t *testing.T) (*LinksStore, *sql.DB) {
	t.Helper()
	pool := dbtest.NewDB(t)
	s, err := NewLinksStore(pool)
	if err != nil {
		t.Fatalf("NewLinksStore: %v", err)
	}
	return s, pool
}

func TestLinksStoreGetAbsent(t *testing.T) {
	s, _ := newLinksStore(t)
	link, err := s.Get(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 0)
	if err != nil {
		t.Fatalf("Get absent: %v", err)
	}
	if link != nil {
		t.Fatalf("Get absent: expected nil link, got %+v", link)
	}
}

func TestLinksStorePendingLifecycle(t *testing.T) {
	ctx := context.Background()
	s, pool := newLinksStore(t)

	const hash = "0123456789abcdef0123456789abcdef01234567"
	if err := s.UpsertPending(ctx, hash, 0, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}

	link, err := s.Get(ctx, hash, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if link == nil {
		t.Fatal("Get: expected link after UpsertPending")
	}
	if link.Status != StatusPending {
		t.Errorf("Status = %q, want %q", link.Status, StatusPending)
	}
	if link.Reason != "" {
		t.Errorf("Reason = %q, want empty", link.Reason)
	}
	if link.Languages != "pt-BR,pt" {
		t.Errorf("Languages = %q, want pt-BR,pt", link.Languages)
	}
	if link.InfoHash != hash || link.FileIndex != 0 {
		t.Errorf("key = (%s, %d), want (%s, 0)", link.InfoHash, link.FileIndex, hash)
	}
	if link.ID == 0 || link.CreatedAt.IsZero() || link.UpdatedAt.IsZero() {
		t.Errorf("expected identity/timestamps populated, got %+v", link)
	}

	// A second pending upsert is idempotent in row count (ON CONFLICT).
	if err := s.UpsertPending(ctx, hash, 0, "pt-BR"); err != nil {
		t.Fatalf("second UpsertPending: %v", err)
	}
	var count int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM subtitle_links`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("rows = %d, want 1 (ON CONFLICT must not duplicate)", count)
	}

	// Different file_index is a different item; a `local:` synthetic hash is
	// just another text key.
	if err := s.UpsertPending(ctx, hash, 1, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending file 1: %v", err)
	}
	if err := s.UpsertPending(ctx, "local:/movies/movie.mkv", 0, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending local: %v", err)
	}
	file1, err := s.Get(ctx, hash, 1)
	if err != nil || file1 == nil {
		t.Fatalf("Get file 1: %v, %v", file1, err)
	}
	if file1.ID == link.ID {
		t.Errorf("file 1 shares the row id with file 0")
	}
	local, err := s.Get(ctx, "local:/movies/movie.mkv", 0)
	if err != nil || local == nil {
		t.Fatalf("Get local: %v, %v", local, err)
	}
	if local.Status != StatusPending {
		t.Errorf("local Status = %q, want %q", local.Status, StatusPending)
	}
}

func TestLinksStoreMarkReadyHashRefines(t *testing.T) {
	ctx := context.Background()
	s, _ := newLinksStore(t)

	const hash = "aaaabbbbccccddddaaaabbbbccccddddaaaabbbb"
	if err := s.UpsertPending(ctx, hash, 2, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	pending, _ := s.Get(ctx, hash, 2)

	if err := s.MarkReady(ctx, hash, 2, "654321", "pt-BR", "pt-BR,pt", true, false,
		"/data/subs/654321.vtt", SourceHash); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	link, err := s.Get(ctx, hash, 2)
	if err != nil || link == nil {
		t.Fatalf("Get after MarkReady: %v, %v", link, err)
	}
	if link.Status != StatusReady {
		t.Errorf("Status = %q, want %q", link.Status, StatusReady)
	}
	if link.Reason != "" {
		t.Errorf("Reason = %q, want empty", link.Reason)
	}
	if link.FileID != "654321" || link.Language != "pt-BR" || link.Languages != "pt-BR,pt" {
		t.Errorf("subtitle fields = (%q, %q, %q)", link.FileID, link.Language, link.Languages)
	}
	if !link.HearingImpaired || link.Forced {
		t.Errorf("flags = (hi=%v, forced=%v), want (true, false)", link.HearingImpaired, link.Forced)
	}
	if link.VTTPath != "/data/subs/654321.vtt" {
		t.Errorf("VTTPath = %q", link.VTTPath)
	}
	if link.Source != SourceHash {
		t.Errorf("Source = %q, want %q", link.Source, SourceHash)
	}
	if !link.HashRefined {
		t.Error("HashRefined = false, want true when source is hash")
	}
	if link.ID != pending.ID || !link.CreatedAt.Equal(pending.CreatedAt) {
		t.Errorf("MarkReady must keep id/created_at: got (%d, %v), want (%d, %v)",
			link.ID, link.CreatedAt, pending.ID, pending.CreatedAt)
	}

	// A non-hash source clears hash_refined.
	if err := s.MarkReady(ctx, hash, 2, "654322", "pt", "pt-BR,pt", false, false,
		"/data/subs/654322.vtt", SourceIMDB); err != nil {
		t.Fatalf("MarkReady imdb: %v", err)
	}
	link, _ = s.Get(ctx, hash, 2)
	if link.Source != SourceIMDB {
		t.Errorf("Source = %q, want %q", link.Source, SourceIMDB)
	}
	if link.HashRefined {
		t.Error("HashRefined = true, want false when source is imdb")
	}
}

func TestLinksStoreMarkReadyWithoutPending(t *testing.T) {
	ctx := context.Background()
	s, _ := newLinksStore(t)

	const hash = "bbbbccccddddaaaabbbbccccddddaaaabbbbcccc"
	// MarkReady without a prior UpsertPending must not lose the decision.
	if err := s.MarkReady(ctx, hash, 0, "111", "pt-BR", "pt-BR", false, false,
		"/data/subs/111.vtt", SourceQuery); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	link, err := s.Get(ctx, hash, 0)
	if err != nil || link == nil {
		t.Fatalf("Get: %v, %v", link, err)
	}
	if link.Status != StatusReady || link.HashRefined {
		t.Errorf("Status/HashRefined = (%q, %v), want (ready, false)", link.Status, link.HashRefined)
	}
}

func TestLinksStoreMarkRefined(t *testing.T) {
	ctx := context.Background()
	s, pool := newLinksStore(t)

	const hash = "aaaabbbbccccddddeeeeffff0000111122223333"
	if err := s.UpsertPending(ctx, hash, 3, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	if err := s.MarkReady(ctx, hash, 3, "654321", "pt-BR", "pt-BR,pt", false, false,
		"/data/subs/654321.vtt", SourceIMDB); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	before, err := s.Get(ctx, hash, 3)
	if err != nil || before == nil {
		t.Fatalf("Get before MarkRefined: %v, %v", before, err)
	}
	time.Sleep(10 * time.Millisecond) // updated_at has microsecond resolution

	// The refine-failure marker: hash_refined flips, everything else stays.
	if err := s.MarkRefined(ctx, hash, 3); err != nil {
		t.Fatalf("MarkRefined: %v", err)
	}
	link, err := s.Get(ctx, hash, 3)
	if err != nil || link == nil {
		t.Fatalf("Get after MarkRefined: %v, %v", link, err)
	}
	if !link.HashRefined {
		t.Error("HashRefined = false, want true after MarkRefined")
	}
	if link.Status != StatusReady || link.Source != SourceIMDB || link.Reason != "" ||
		link.FileID != before.FileID || link.Language != before.Language ||
		link.Languages != before.Languages || link.VTTPath != before.VTTPath ||
		link.HearingImpaired != before.HearingImpaired || link.Forced != before.Forced {
		t.Errorf("MarkRefined altered the old decision:\n got %+v\nwant %+v", link, before)
	}
	if link.ID != before.ID || !link.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("MarkRefined must keep id/created_at")
	}
	if !link.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("updated_at = %v, want bumped (was %v)", link.UpdatedAt, before.UpdatedAt)
	}

	// Idempotent repeat.
	if err := s.MarkRefined(ctx, hash, 3); err != nil {
		t.Fatalf("second MarkRefined: %v", err)
	}

	// Only existing rows: an absent key is an error, never an upsert.
	if err := s.MarkRefined(ctx, hash, 9); err == nil {
		t.Error("MarkRefined on absent row = nil error, want error (must not create a row)")
	}
	if row, _ := s.Get(ctx, hash, 9); row != nil {
		t.Errorf("MarkRefined invented a row: %+v", row)
	}
	var count int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM subtitle_links WHERE info_hash = $1`, hash).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("rows = %d, want 1 (MarkRefined never upserts)", count)
	}
}

func TestLinksStoreMarkFailed(t *testing.T) {
	ctx := context.Background()
	s, _ := newLinksStore(t)

	const hash = "ccccddddaaaabbbbccccddddaaaabbbbccccdddd"
	if err := s.UpsertPending(ctx, hash, 0, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	pending, _ := s.Get(ctx, hash, 0)

	if err := s.MarkFailed(ctx, hash, 0, ReasonNoResults); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	link, err := s.Get(ctx, hash, 0)
	if err != nil || link == nil {
		t.Fatalf("Get: %v, %v", link, err)
	}
	if link.Status != StatusFailed || link.Reason != ReasonNoResults {
		t.Errorf("Status/Reason = (%q, %q), want (failed, no_results)", link.Status, link.Reason)
	}
	if link.ID != pending.ID || !link.CreatedAt.Equal(pending.CreatedAt) {
		t.Errorf("MarkFailed must keep id/created_at")
	}

	// MarkFailed on a key with no prior row also persists (upsert).
	if err := s.MarkFailed(ctx, hash, 9, ReasonQuota); err != nil {
		t.Fatalf("MarkFailed fresh key: %v", err)
	}
	fresh, err := s.Get(ctx, hash, 9)
	if err != nil || fresh == nil {
		t.Fatalf("Get fresh failed key: %v, %v", fresh, err)
	}
	if fresh.Status != StatusFailed || fresh.Reason != ReasonQuota {
		t.Errorf("Status/Reason = (%q, %q), want (failed, quota)", fresh.Status, fresh.Reason)
	}
}

func TestLinksStoreRependingResetsStatusKeepsIdentity(t *testing.T) {
	ctx := context.Background()
	s, _ := newLinksStore(t)

	const hash = "ddddaaaabbbbccccddddaaaabbbbccccddddaaaa"
	if err := s.UpsertPending(ctx, hash, 0, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	if err := s.MarkReady(ctx, hash, 0, "777", "pt-BR", "pt-BR,pt", false, false,
		"/data/subs/777.vtt", SourceHash); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	ready, _ := s.Get(ctx, hash, 0)

	// Settings changed → one re-search: reset to pending, update the language
	// list, keep identity and the hash_refined/vtt_path outcome history.
	if err := s.UpsertPending(ctx, hash, 0, "pt-BR,pt,es"); err != nil {
		t.Fatalf("re-UpsertPending: %v", err)
	}
	link, err := s.Get(ctx, hash, 0)
	if err != nil || link == nil {
		t.Fatalf("Get: %v, %v", link, err)
	}
	if link.Status != StatusPending {
		t.Errorf("Status = %q, want %q", link.Status, StatusPending)
	}
	if link.Reason != "" {
		t.Errorf("Reason = %q, want empty after reset", link.Reason)
	}
	if link.Languages != "pt-BR,pt,es" {
		t.Errorf("Languages = %q, want pt-BR,pt,es", link.Languages)
	}
	if link.ID != ready.ID || !link.CreatedAt.Equal(ready.CreatedAt) {
		t.Errorf("re-pending must keep id/created_at")
	}
	if !link.HashRefined || link.VTTPath != ready.VTTPath {
		t.Errorf("re-pending must keep hash_refined/vtt_path: got (%v, %q)",
			link.HashRefined, link.VTTPath)
	}
}

func TestLinksStoreConcurrentUpsertPending(t *testing.T) {
	ctx := context.Background()
	s, pool := newLinksStore(t)

	const hash = "eeeeaaaabbbbcccceeeeaaaabbbbcccceeeeaaaa"
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.UpsertPending(ctx, hash, 0, "pt-BR,pt"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent UpsertPending: %v", err)
	}

	var count int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM subtitle_links WHERE info_hash = $1`, hash).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("rows = %d, want 1", count)
	}
	link, _ := s.Get(ctx, hash, 0)
	if link == nil || link.Status != StatusPending {
		t.Errorf("final link = %+v, want pending", link)
	}
}

func TestLinksStoreValidateStatus(t *testing.T) {
	valid := []string{StatusPending, StatusReady, StatusFailed}
	for _, status := range valid {
		if err := validateStatus(status); err != nil {
			t.Errorf("validateStatus(%q) = %v, want nil", status, err)
		}
	}
	for _, status := range []string{"", "Pending", "done", "failed ", "readyx"} {
		if err := validateStatus(status); err == nil {
			t.Errorf("validateStatus(%q) = nil, want error", status)
		}
	}
}

func TestLinksStoreNilSafe(t *testing.T) {
	ctx := context.Background()
	var s *LinksStore

	link, err := s.Get(ctx, "hash", 0)
	if err != nil || link != nil {
		t.Errorf("nil store Get = (%v, %v), want (nil, nil)", link, err)
	}
	if err := s.UpsertPending(ctx, "hash", 0, "pt-BR"); err != nil {
		t.Errorf("nil store UpsertPending = %v, want nil", err)
	}
	if err := s.MarkReady(ctx, "hash", 0, "1", "pt-BR", "pt-BR", false, false, "/x.vtt", SourceHash); err != nil {
		t.Errorf("nil store MarkReady = %v, want nil", err)
	}
	if err := s.MarkFailed(ctx, "hash", 0, ReasonError); err != nil {
		t.Errorf("nil store MarkFailed = %v, want nil", err)
	}
	if err := s.MarkRefined(ctx, "hash", 0); err != nil {
		t.Errorf("nil store MarkRefined = %v, want nil", err)
	}

	// Real store, empty key: documented no-op.
	real, _ := newLinksStore(t)
	if err := real.UpsertPending(ctx, "", 0, "pt-BR"); err != nil {
		t.Errorf("empty infoHash UpsertPending = %v, want nil", err)
	}
	if err := real.MarkFailed(ctx, "", 0, ReasonError); err != nil {
		t.Errorf("empty infoHash MarkFailed = %v, want nil", err)
	}
	if err := real.MarkRefined(ctx, "", 0); err != nil {
		t.Errorf("empty infoHash MarkRefined = %v, want nil", err)
	}
}

func TestLinksStoreClosedPool(t *testing.T) {
	ctx := context.Background()
	s, pool := newLinksStore(t)
	_ = pool.Close()

	if _, err := s.Get(ctx, "hash", 0); err == nil {
		t.Error("Get on closed pool = nil error, want error")
	}
	if err := s.UpsertPending(ctx, "hash", 0, "pt-BR"); err == nil {
		t.Error("UpsertPending on closed pool = nil error, want error")
	}
	if err := s.MarkReady(ctx, "hash", 0, "1", "pt-BR", "pt-BR", false, false, "/x.vtt", SourceHash); err == nil {
		t.Error("MarkReady on closed pool = nil error, want error")
	}
	if err := s.MarkRefined(ctx, "hash", 0); err == nil {
		t.Error("MarkRefined on closed pool = nil error, want error")
	}
	if err := s.MarkFailed(ctx, "hash", 0, ReasonError); err == nil {
		t.Error("MarkFailed on closed pool = nil error, want error")
	}
}
