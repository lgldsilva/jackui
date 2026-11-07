package subtitles

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Store-level tests over the in-memory SQL fake (no PostgreSQL needed — the
// gate and PR CI run without JACKUI_TEST_DATABASE_URL). They pin the store's
// own behavior: column order of the Scan, the exact status/reason/languages
// parameters each writer persists, error wrapping, and the no-op guards.
// The real-SQL upsert semantics are re-verified by the dbtest-gated tests in
// links_test.go whenever a test database is available.

func TestLinksFakeLifecycleRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, fdb := newFakeLinksStore(t)
	const key = "dddd1111dddd1111dddd1111dddd1111dddd1111" // fake key, gitleaks-quiet

	// Absent key: (nil, nil), like the real store's sql.ErrNoRows mapping.
	row, err := s.Get(ctx, key, 0)
	if err != nil || row != nil {
		t.Fatalf("Get absent = (%v, %v), want (nil, nil)", row, err)
	}

	if err := s.UpsertPending(ctx, key, 0, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	pending, err := s.Get(ctx, key, 0)
	if err != nil || pending == nil {
		t.Fatalf("Get pending: %v, %v", pending, err)
	}
	if pending.Status != StatusPending || pending.Reason != "" || pending.Languages != "pt-BR,pt" {
		t.Errorf("pending row = %+v", pending)
	}
	if pending.InfoHash != key || pending.FileIndex != 0 {
		t.Errorf("key not round-tripped: %+v", pending)
	}
	if pending.ID == 0 || pending.CreatedAt.IsZero() || pending.UpdatedAt.IsZero() {
		t.Errorf("identity/timestamps missing: %+v", pending)
	}

	// MarkReady persists every outcome field with the given values; a hash
	// source sets hash_refined. Distinct values per column pin the Scan order.
	if err := s.MarkReady(ctx, key, 0, "654321", "pt-BR", "pt-BR,pt", true, true,
		"/cache/654321.vtt", SourceHash); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	ready, err := s.Get(ctx, key, 0)
	if err != nil || ready == nil {
		t.Fatalf("Get ready: %v, %v", ready, err)
	}
	if ready.Status != StatusReady || ready.Reason != "" {
		t.Errorf("status/reason = %q/%q, want ready/\"\"", ready.Status, ready.Reason)
	}
	if ready.FileID != "654321" || ready.Language != "pt-BR" || ready.Languages != "pt-BR,pt" {
		t.Errorf("file/language fields = %q/%q/%q", ready.FileID, ready.Language, ready.Languages)
	}
	if !ready.HearingImpaired || !ready.Forced {
		t.Errorf("flags = %v/%v, want true/true", ready.HearingImpaired, ready.Forced)
	}
	if ready.VTTPath != "/cache/654321.vtt" || ready.Source != SourceHash || !ready.HashRefined {
		t.Errorf("path/source/refined = %q/%q/%v", ready.VTTPath, ready.Source, ready.HashRefined)
	}
	if ready.ID != pending.ID || !ready.CreatedAt.Equal(pending.CreatedAt) {
		t.Errorf("MarkReady must keep id/created_at: (%d, %v) vs (%d, %v)",
			ready.ID, ready.CreatedAt, pending.ID, pending.CreatedAt)
	}

	// A non-hash source clears hash_refined (the column is a plain parameter,
	// not an accumulator).
	if err := s.MarkReady(ctx, key, 0, "654322", "pt", "pt-BR,pt", false, false,
		"/cache/654322.vtt", SourceIMDB); err != nil {
		t.Fatalf("MarkReady imdb: %v", err)
	}
	row, _ = s.Get(ctx, key, 0)
	if row.HashRefined || row.Source != SourceIMDB {
		t.Errorf("imdb MarkReady left hash_refined=%v source=%q", row.HashRefined, row.Source)
	}

	// MarkRefined flags the existing row without touching the decision.
	before := fdb.snapshot(key, 0)
	time.Sleep(2 * time.Millisecond)
	if err := s.MarkRefined(ctx, key, 0); err != nil {
		t.Fatalf("MarkRefined: %v", err)
	}
	row, _ = s.Get(ctx, key, 0)
	if !row.HashRefined {
		t.Error("MarkRefined did not set hash_refined")
	}
	if row.Status != before.Status || row.FileID != before.FileID || row.VTTPath != before.VTTPath ||
		row.Source != before.Source || row.Languages != before.Languages {
		t.Errorf("MarkRefined altered the decision: %+v (was %+v)", row, before)
	}

	// MarkFailed sets status/reason only; outcome fields survive for debugging.
	if err := s.MarkFailed(ctx, key, 0, ReasonQuota); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	row, _ = s.Get(ctx, key, 0)
	if row.Status != StatusFailed || row.Reason != ReasonQuota {
		t.Errorf("status/reason = %q/%q, want failed/quota", row.Status, row.Reason)
	}
	if row.FileID != "654322" || row.VTTPath != "/cache/654322.vtt" {
		t.Errorf("MarkFailed dropped outcome fields: %+v", row)
	}

	// UpsertPending over a finished row resets it to pending with the new
	// language list, clears the reason, and keeps id/created_at.
	if err := s.UpsertPending(ctx, key, 0, "en,es"); err != nil {
		t.Fatalf("re-UpsertPending: %v", err)
	}
	row, _ = s.Get(ctx, key, 0)
	if row.Status != StatusPending || row.Reason != "" || row.Languages != "en,es" {
		t.Errorf("re-pending row = %+v", row)
	}
	if row.ID != before.ID || !row.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("re-pending must keep id/created_at")
	}
}

func TestLinksFakeExecParameters(t *testing.T) {
	ctx := context.Background()
	s, fdb := newFakeLinksStore(t)
	const key = "aaaa0000aaaa0000aaaa0000aaaa0000aaaa0000"

	if err := s.UpsertPending(ctx, key, 3, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	execs := fdb.recordedExecs()
	if len(execs) != 1 {
		t.Fatalf("execs = %d, want 1", len(execs))
	}
	if !strings.HasPrefix(execs[0].SQL, "INSERT INTO subtitle_links") {
		t.Errorf("UpsertPending SQL = %q", execs[0].SQL)
	}
	if got := execs[0].Args; got[0] != key || got[1] != int64(3) || got[2] != StatusPending || got[3] != "pt-BR,pt" {
		t.Errorf("UpsertPending args = %v", got)
	}

	if err := s.MarkReady(ctx, key, 3, "777", "pt-BR", "pt-BR,pt", false, true,
		"/c.vtt", SourceQuery); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	execs = fdb.recordedExecs()
	args := execs[1].Args
	// hash_refined must be derived from source == SourceHash, not passed verbatim.
	if args[0] != key || args[1] != int64(3) || args[2] != StatusReady {
		t.Errorf("MarkReady key/status args = %v", args[:3])
	}
	if args[3] != "777" || args[4] != "pt-BR" || args[5] != "pt-BR,pt" {
		t.Errorf("MarkReady file args = %v", args[3:6])
	}
	if args[6] != false || args[7] != true || args[8] != "/c.vtt" || args[9] != SourceQuery {
		t.Errorf("MarkReady outcome args = %v", args[6:10])
	}
	if args[10] != false {
		t.Errorf("hash_refined = %v with source query, want false", args[10])
	}

	if err := s.MarkReady(ctx, key, 3, "778", "pt", "pt", false, false, "/d.vtt", SourceHash); err != nil {
		t.Fatalf("MarkReady hash: %v", err)
	}
	if got := fdb.recordedExecs()[2].Args[10]; got != true {
		t.Errorf("hash_refined = %v with source hash, want true", got)
	}

	if err := s.MarkRefined(ctx, key, 3); err != nil {
		t.Fatalf("MarkRefined: %v", err)
	}
	refineExec := fdb.recordedExecs()[3]
	if !strings.HasPrefix(refineExec.SQL, "UPDATE subtitle_links") {
		t.Errorf("MarkRefined SQL = %q", refineExec.SQL)
	}
	if refineExec.Args[0] != key || refineExec.Args[1] != int64(3) {
		t.Errorf("MarkRefined args = %v", refineExec.Args)
	}

	if err := s.MarkFailed(ctx, key, 3, ReasonNoResults); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	failExec := fdb.recordedExecs()[4]
	if failExec.Args[2] != StatusFailed || failExec.Args[3] != ReasonNoResults {
		t.Errorf("MarkFailed args = %v", failExec.Args)
	}
}

func TestLinksFakeGetQueryError(t *testing.T) {
	ctx := context.Background()
	s, fdb := newFakeLinksStore(t)
	boom := errors.New("pool gone")
	fdb.failQueries(boom)
	_, err := s.Get(ctx, "hash", 0)
	if err == nil || !errors.Is(err, boom) || !strings.Contains(err.Error(), "get subtitle link hash#0") {
		t.Errorf("Get error = %v, want wrapped pool error with key context", err)
	}
}

func TestLinksFakeWriteErrorsWrapped(t *testing.T) {
	ctx := context.Background()
	s, fdb := newFakeLinksStore(t)
	boom := errors.New("disk on fire")
	fdb.failExecs(boom)

	if err := s.UpsertPending(ctx, "hash", 0, "pt-BR"); err == nil ||
		!errors.Is(err, boom) || !strings.Contains(err.Error(), "upsert pending subtitle link hash#0") {
		t.Errorf("UpsertPending error = %v", err)
	}
	if err := s.MarkReady(ctx, "hash", 0, "1", "pt", "pt", false, false, "/v", SourceHash); err == nil ||
		!errors.Is(err, boom) || !strings.Contains(err.Error(), "mark subtitle link ready") {
		t.Errorf("MarkReady error = %v", err)
	}
	if err := s.MarkRefined(ctx, "hash", 0); err == nil ||
		!errors.Is(err, boom) || !strings.Contains(err.Error(), "mark subtitle link refined") {
		t.Errorf("MarkRefined error = %v", err)
	}
	if err := s.MarkFailed(ctx, "hash", 0, ReasonError); err == nil ||
		!errors.Is(err, boom) || !strings.Contains(err.Error(), "mark subtitle link failed") {
		t.Errorf("MarkFailed error = %v", err)
	}
}

func TestLinksFakeMarkRefinedRequiresRow(t *testing.T) {
	ctx := context.Background()
	s, fdb := newFakeLinksStore(t)
	const key = "bbbb0000bbbb0000bbbb0000bbbb0000bbbb0000"

	// Not an upsert: an absent row is an error, never an invented one.
	if err := s.MarkRefined(ctx, key, 9); err == nil || !strings.Contains(err.Error(), "no such row") {
		t.Errorf("MarkRefined absent = %v, want no-such-row error", err)
	}
	if fdb.snapshot(key, 9) != nil {
		t.Error("MarkRefined invented a row")
	}

	if err := s.UpsertPending(ctx, key, 1, "pt-BR"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	if err := s.MarkRefined(ctx, key, 1); err != nil {
		t.Fatalf("MarkRefined existing: %v", err)
	}
	if row := fdb.snapshot(key, 1); !row.HashRefined {
		t.Error("MarkRefined did not persist the flag")
	}
}

func TestLinksFakeEmptyKeyNoOps(t *testing.T) {
	ctx := context.Background()
	s, fdb := newFakeLinksStore(t)
	if err := s.UpsertPending(ctx, "", 0, "pt-BR"); err != nil {
		t.Errorf("UpsertPending empty key = %v, want nil", err)
	}
	if err := s.MarkReady(ctx, "", 0, "1", "pt", "pt", false, false, "/v", SourceHash); err != nil {
		t.Errorf("MarkReady empty key = %v, want nil", err)
	}
	if err := s.MarkRefined(ctx, "", 0); err != nil {
		t.Errorf("MarkRefined empty key = %v, want nil", err)
	}
	if err := s.MarkFailed(ctx, "", 0, ReasonError); err != nil {
		t.Errorf("MarkFailed empty key = %v, want nil", err)
	}
	if row, err := s.Get(ctx, "", 0); err != nil || row != nil {
		t.Errorf("Get empty key = (%v, %v), want (nil, nil)", row, err)
	}
	if ex := fdb.recordedExecs(); len(ex) != 0 {
		t.Errorf("empty-key calls issued execs: %+v", ex)
	}
}
