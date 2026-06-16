package ai

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *BenchmarkStore {
	t.Helper()
	s, err := NewBenchmarkStore(filepath.Join(t.TempDir(), "bench.db"))
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedHistory writes a known prior history state directly so the transition tests
// can assert timestamp PRESERVATION robustly (RecordRun's own time.Now is
// second-granular, so two calls in the same test second wouldn't prove it).
func seedHistory(t *testing.T, s *BenchmarkStore, slot, outcome, successAt, firstFailAt string, consec int) {
	t.Helper()
	var sa, ff any
	if successAt != "" {
		sa = successAt
	}
	if firstFailAt != "" {
		ff = firstFailAt
	}
	_, err := s.db.Exec(`
		INSERT INTO benchmark_history(slot_id, last_outcome, last_error, last_success_at, last_run_at, first_failure_at, consecutive_failures)
		VALUES(?, ?, '', ?, ?, ?, ?)`, slot, outcome, sa, successAt, ff, consec)
	if err != nil {
		t.Fatalf("seedHistory: %v", err)
	}
}

func resultBySlot(t *testing.T, s *BenchmarkStore, slot string) SlotScore {
	t.Helper()
	for _, r := range s.Results() {
		if r.SlotID == slot {
			return r
		}
	}
	t.Fatalf("slot %q not in results", slot)
	return SlotScore{}
}

func TestRunOutcome(t *testing.T) {
	cases := []struct {
		name string
		s    SlotScore
		want string
	}{
		{"complete usable run", SlotScore{Samples: 5}, OutcomeOK},
		{"transiently cut short", SlotScore{Incomplete: true, Samples: 2}, OutcomeIncomplete},
		{"incomplete with zero samples", SlotScore{Incomplete: true}, OutcomeIncomplete},
		{"hard failure no reply", SlotScore{Samples: 0, FailureReason: "boom"}, OutcomeError},
	}
	for _, tc := range cases {
		if got := RunOutcome(tc.s); got != tc.want {
			t.Errorf("%s: RunOutcome = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestRecordRunFirstError: a model that has never run, then errors, gets a fresh
// failure streak (consec 1, first_failure_at set) and no last_success_at.
func TestRecordRunFirstError(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveResults([]SlotScore{{SlotID: "p:m", Provider: "p", Model: "m"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRun([]SlotScore{{SlotID: "p:m", Samples: 0, FailureReason: "boom"}}); err != nil {
		t.Fatal(err)
	}
	r := resultBySlot(t, s, "p:m")
	if r.LastOutcome != OutcomeError {
		t.Errorf("LastOutcome = %q, want error", r.LastOutcome)
	}
	if r.ConsecutiveFailures != 1 {
		t.Errorf("ConsecutiveFailures = %d, want 1", r.ConsecutiveFailures)
	}
	if r.LastSuccessAt != "" {
		t.Errorf("LastSuccessAt = %q, want empty (never succeeded)", r.LastSuccessAt)
	}
	if r.FirstFailureAt == "" || r.LastRunAt == "" {
		t.Errorf("FirstFailureAt/LastRunAt should be set, got %q / %q", r.FirstFailureAt, r.LastRunAt)
	}
}

// TestRecordRunErrorPersists: a second error extends the streak and PRESERVES the
// original first_failure_at (the date the error started) — that's "o erro se manteve".
func TestRecordRunErrorPersists(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveResults([]SlotScore{{SlotID: "p:m"}}); err != nil {
		t.Fatal(err)
	}
	seedHistory(t, s, "p:m", OutcomeError, "", "2020-01-01 00:00:00", 1)
	if err := s.RecordRun([]SlotScore{{SlotID: "p:m", Samples: 0, FailureReason: "boom2"}}); err != nil {
		t.Fatal(err)
	}
	r := resultBySlot(t, s, "p:m")
	if r.ConsecutiveFailures != 2 {
		t.Errorf("ConsecutiveFailures = %d, want 2", r.ConsecutiveFailures)
	}
	if r.FirstFailureAt != "2020-01-01T00:00:00Z" {
		t.Errorf("FirstFailureAt = %q, want preserved 2020-01-01T00:00:00Z", r.FirstFailureAt)
	}
	if r.LastSuccessAt != "" {
		t.Errorf("LastSuccessAt = %q, want still empty", r.LastSuccessAt)
	}
}

// TestRecordRunRecovers: a usable run after a failure streak clears the streak and
// stamps last_success_at — that's "qual a data da última vez que deu certo".
func TestRecordRunRecovers(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveResults([]SlotScore{{SlotID: "p:m"}}); err != nil {
		t.Fatal(err)
	}
	seedHistory(t, s, "p:m", OutcomeError, "", "2020-01-01 00:00:00", 3)
	if err := s.RecordRun([]SlotScore{{SlotID: "p:m", Samples: 4, Accuracy: 1}}); err != nil {
		t.Fatal(err)
	}
	r := resultBySlot(t, s, "p:m")
	if r.LastOutcome != OutcomeOK {
		t.Errorf("LastOutcome = %q, want ok", r.LastOutcome)
	}
	if r.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0", r.ConsecutiveFailures)
	}
	if r.FirstFailureAt != "" {
		t.Errorf("FirstFailureAt = %q, want cleared", r.FirstFailureAt)
	}
	if r.LastSuccessAt == "" {
		t.Error("LastSuccessAt should be stamped on a usable run")
	}
}

// TestRecordRunErrorAfterSuccessPreservesLastSuccess: when a model that previously
// succeeded errors, the last_success_at is kept — the UI still shows "last OK: …".
func TestRecordRunErrorAfterSuccessPreservesLastSuccess(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveResults([]SlotScore{{SlotID: "p:m"}}); err != nil {
		t.Fatal(err)
	}
	seedHistory(t, s, "p:m", OutcomeOK, "2019-05-05 12:00:00", "", 0)
	if err := s.RecordRun([]SlotScore{{SlotID: "p:m", Samples: 0, FailureReason: "boom"}}); err != nil {
		t.Fatal(err)
	}
	r := resultBySlot(t, s, "p:m")
	if r.LastSuccessAt != "2019-05-05T12:00:00Z" {
		t.Errorf("LastSuccessAt = %q, want preserved 2019-05-05T12:00:00Z", r.LastSuccessAt)
	}
	if r.ConsecutiveFailures != 1 || r.FirstFailureAt == "" {
		t.Errorf("a fresh streak should start: consec=%d firstFail=%q", r.ConsecutiveFailures, r.FirstFailureAt)
	}
}

// TestRecordRunIncompleteResetsStreak: a rate-limited (incomplete) run isn't a hard
// error, so it clears the failure streak but does NOT stamp a new success.
func TestRecordRunIncompleteResetsStreak(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveResults([]SlotScore{{SlotID: "p:m"}}); err != nil {
		t.Fatal(err)
	}
	seedHistory(t, s, "p:m", OutcomeError, "2019-05-05 12:00:00", "2020-01-01 00:00:00", 2)
	if err := s.RecordRun([]SlotScore{{SlotID: "p:m", Incomplete: true, FailureReason: "rate limited"}}); err != nil {
		t.Fatal(err)
	}
	r := resultBySlot(t, s, "p:m")
	if r.LastOutcome != OutcomeIncomplete {
		t.Errorf("LastOutcome = %q, want incomplete", r.LastOutcome)
	}
	if r.ConsecutiveFailures != 0 || r.FirstFailureAt != "" {
		t.Errorf("streak should reset: consec=%d firstFail=%q", r.ConsecutiveFailures, r.FirstFailureAt)
	}
	if r.LastSuccessAt != "2019-05-05T12:00:00Z" {
		t.Errorf("LastSuccessAt = %q, want preserved (incomplete isn't a success)", r.LastSuccessAt)
	}
}

// TestRecordRunLeavesCarriedSlotUntouched: recording the freshly-run slot must NOT
// touch the history of a slot that wasn't re-run (else its streak bumps for nothing).
func TestRecordRunLeavesCarriedSlotUntouched(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveResults([]SlotScore{{SlotID: "p:a"}, {SlotID: "p:b"}}); err != nil {
		t.Fatal(err)
	}
	seedHistory(t, s, "p:b", OutcomeOK, "2019-05-05 12:00:00", "", 0)
	if err := s.RecordRun([]SlotScore{{SlotID: "p:a", Samples: 0, FailureReason: "boom"}}); err != nil {
		t.Fatal(err)
	}
	b := resultBySlot(t, s, "p:b")
	if b.LastOutcome != OutcomeOK || b.LastSuccessAt != "2019-05-05T12:00:00Z" {
		t.Errorf("carried slot history changed: %+v", b)
	}
	a := resultBySlot(t, s, "p:a")
	if a.LastOutcome != OutcomeError {
		t.Errorf("re-run slot should be recorded as error, got %q", a.LastOutcome)
	}
}

// TestResultsNoHistoryIsGraceful: a result with no history row (legacy) decodes to
// empty history fields rather than erroring.
func TestResultsNoHistoryIsGraceful(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveResults([]SlotScore{{SlotID: "p:m", Samples: 1, Accuracy: 1}}); err != nil {
		t.Fatal(err)
	}
	r := resultBySlot(t, s, "p:m")
	if r.LastOutcome != "" || r.LastSuccessAt != "" || r.ConsecutiveFailures != 0 {
		t.Errorf("legacy row should have empty history, got %+v", r)
	}
}

// TestRecordRunErrorStreakWithoutPriorStart: an error streak whose first_failure_at
// was never recorded (legacy/partial) gets one stamped now while still extending.
func TestRecordRunErrorStreakWithoutPriorStart(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveResults([]SlotScore{{SlotID: "p:m"}}); err != nil {
		t.Fatal(err)
	}
	seedHistory(t, s, "p:m", OutcomeError, "", "", 1) // error streak, no first_failure_at
	if err := s.RecordRun([]SlotScore{{SlotID: "p:m", Samples: 0, FailureReason: "boom"}}); err != nil {
		t.Fatal(err)
	}
	r := resultBySlot(t, s, "p:m")
	if r.ConsecutiveFailures != 2 {
		t.Errorf("ConsecutiveFailures = %d, want 2", r.ConsecutiveFailures)
	}
	if r.FirstFailureAt == "" {
		t.Error("FirstFailureAt should be backfilled to now when the streak had none")
	}
}

func TestTsRFC3339(t *testing.T) {
	if got := tsRFC3339(sql.NullString{}); got != "" {
		t.Errorf("null → %q, want empty", got)
	}
	if got := tsRFC3339(sql.NullString{Valid: true, String: ""}); got != "" {
		t.Errorf("empty → %q, want empty", got)
	}
	if got := tsRFC3339(sql.NullString{Valid: true, String: "not-a-time"}); got != "" {
		t.Errorf("unparseable → %q, want empty", got)
	}
	if got := tsRFC3339(sql.NullString{Valid: true, String: "2020-01-02 03:04:05"}); got != "2020-01-02T03:04:05Z" {
		t.Errorf("valid → %q, want 2020-01-02T03:04:05Z", got)
	}
}

func TestRecordRunNilSafe(t *testing.T) {
	var s *BenchmarkStore
	if err := s.RecordRun([]SlotScore{{SlotID: "x"}}); err != nil {
		t.Errorf("nil store RecordRun should be a no-op, got %v", err)
	}
	live := newTestStore(t)
	if err := live.RecordRun(nil); err != nil {
		t.Errorf("empty RecordRun should be a no-op, got %v", err)
	}
}
