package ai

import (
	"context"
	"errors"
	"testing"
)

func TestBenchmarkStoreCallsHonourCancelledContext(t *testing.T) {
	s := newTestStore(t)
	live := context.Background()
	if err := s.SaveResults(live, []SlotScore{{SlotID: "a", Accuracy: 1}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sc := SlotScore{SlotID: "b", Accuracy: 0.5}
	calls := map[string]func() error{
		"SaveResults":    func() error { return s.SaveResults(ctx, []SlotScore{sc}) },
		"RecordRun":      func() error { return s.RecordRun(ctx, []SlotScore{sc}) },
		"RecordOne":      func() error { return s.RecordOne(ctx, sc) },
		"UpsertResult":   func() error { return s.UpsertResult(ctx, sc) },
		"SetCases":       func() error { return s.SetCases(ctx, []BenchmarkCase{{Raw: "r", Expect: "e"}}) },
		"SaveCostConfig": func() error { return s.SaveCostConfig(ctx, CostConfig{MaxCostPer1M: 1}) },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want context.Canceled", name, err)
		}
	}
	// The error-swallowing readers degrade to empty on a done ctx.
	if got := s.Results(ctx); len(got) != 0 {
		t.Errorf("Results on a cancelled ctx = %d rows", len(got))
	}
	if got := s.Order(ctx); len(got) != 0 {
		t.Errorf("Order on a cancelled ctx = %v", got)
	}
	if _, ok := s.LoadCostConfig(ctx); ok {
		t.Error("LoadCostConfig on a cancelled ctx must report not found")
	}
	if got := s.Results(live); len(got) != 1 || got[0].SlotID != "a" {
		t.Fatalf("results after cancelled calls = %+v; want only the original row", got)
	}
}
