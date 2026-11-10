package ai

import (
	"io"
	"testing"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

// Regression (#200): a read that failed mid-iteration was taken as "nothing
// stored". Cases() then re-seeded the defaults, OVERWRITING the user's edited
// case set; the other readers returned a truncated result as if complete.
func TestBenchmarkStore_IterationErrorIsNotEmpty(t *testing.T) {
	f := dbtest.NewFailRowsDB(t, nil, io.ErrUnexpectedEOF)
	s, _ := NewBenchmarkStore(f.DB)

	if got := s.Cases(); len(got) != len(AllDefaultBenchmarkCases()) {
		t.Fatalf("Cases() = %d cases, want the in-memory defaults", len(got))
	}
	if n := f.Execs(); n != 0 {
		t.Fatalf("Cases() wrote %d statements after a failed read — would overwrite the user's set", n)
	}
	if _, found := s.LoadCostConfig(); found {
		t.Fatal("LoadCostConfig must report not-found on a failed read")
	}
}

func TestBenchmarkStore_PartialReadIsDiscarded(t *testing.T) {
	f := dbtest.NewFailRowsDB(t, [][]any{{"slot-a"}}, io.ErrUnexpectedEOF)
	s, _ := NewBenchmarkStore(f.DB)
	if got := s.Order(); got != nil {
		t.Fatalf("Order() = %v, want nil (a truncated chain order must not be applied)", got)
	}

	f2 := dbtest.NewFailRowsDB(t, [][]any{{"max_cost_per_1m", 2.5}}, io.ErrUnexpectedEOF)
	s2, _ := NewBenchmarkStore(f2.DB)
	if cc, found := s2.LoadCostConfig(); found {
		t.Fatalf("LoadCostConfig = %+v,true, want not-found on a truncated read", cc)
	}

	f3 := dbtest.NewFailRowsDB(t, [][]any{{"r", "e", "rename", "user"}}, io.ErrUnexpectedEOF)
	s3, _ := NewBenchmarkStore(f3.DB)
	if got := s3.Cases(); len(got) == 1 {
		t.Fatal("Cases() returned a truncated user set as if complete")
	}
	if f3.Execs() != 0 {
		t.Fatal("Cases() must not write after a truncated read")
	}
}
