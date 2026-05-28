package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luizg/jackui/internal/config"
)

func TestTitleAccuracy(t *testing.T) {
	if a := titleAccuracy("The Matrix", "the.matrix"); a != 1 {
		t.Fatalf("normalized exact match should be 1, got %v", a)
	}
	if a := titleAccuracy("Dune", "Dune Part Two"); !(a > 0 && a < 1) {
		t.Fatalf("partial overlap should be in (0,1), got %v", a)
	}
	if a := titleAccuracy("Totally Wrong", "Inception"); a != 0 {
		t.Fatalf("no overlap should be 0, got %v", a)
	}
}

func TestCompositeScoreFavorsFastAccurate(t *testing.T) {
	fastAccurate := compositeScore(0.9, 400)
	slowAccurate := compositeScore(0.9, 4000)
	fastSloppy := compositeScore(0.4, 400)
	if !(fastAccurate > slowAccurate) {
		t.Fatalf("faster should score higher at equal accuracy: %v vs %v", fastAccurate, slowAccurate)
	}
	if !(fastAccurate > fastSloppy) {
		t.Fatalf("more accurate should score higher at equal latency: %v vs %v", fastAccurate, fastSloppy)
	}
}

func TestRunSortsByComposite(t *testing.T) {
	// Both slots are equally accurate; the only difference is the model id echoed
	// back, so accuracy ties and the sort is stable. We assert Run produces a
	// score per slot and they're ordered by composite descending.
	good := httptest.NewServer(jsonChat(`{"title":"Inception","year":2010,"kind":"movie"}`, http.StatusOK))
	defer good.Close()
	bad := httptest.NewServer(jsonChat("", http.StatusInternalServerError))
	defer bad.Close()

	c := clientForURL(t, good.URL, bad.URL) // p0 works, p1 always fails
	scores := c.Run(context.Background(), []BenchmarkCase{{Raw: "Inception.2010.1080p", Expect: "Inception"}})
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}
	if scores[0].SlotID != "p0" {
		t.Fatalf("working slot should rank first, got %q", scores[0].SlotID)
	}
	if scores[0].Composite <= scores[1].Composite {
		t.Fatalf("scores not sorted desc: %v", scores)
	}
	if scores[1].FailureReason == "" {
		t.Fatal("failing slot should record a failure reason")
	}
}

func TestApplyOrder(t *testing.T) {
	c := clientForURL(t, "http://a", "http://b", "http://c") // p0, p1, p2
	c.ApplyOrder([]string{"p2", "p0"})
	if c.slots[0].ID != "p2" || c.slots[1].ID != "p0" {
		t.Fatalf("order not applied: %s, %s, %s", c.slots[0].ID, c.slots[1].ID, c.slots[2].ID)
	}
	if c.slots[2].ID != "p1" {
		t.Fatalf("unranked slot should fall to the end, got %q", c.slots[2].ID)
	}
}

func TestHealProviderReplacesDeadModel(t *testing.T) {
	// Provider whose /models lists only "goodmodel" — the chain's "deadmodel"
	// must be dropped and replaced.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" || strings.HasSuffix(r.URL.Path, "/models") {
			w.Write([]byte(`{"data":[{"id":"goodmodel"}]}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	cfg := config.AIConfig{Enabled: true, Providers: map[string]config.AIProvider{
		"groq": {BaseURL: srv.URL, APIKey: "k"},
	}, Chain: []config.AIChainSlot{{ID: "groq:deadmodel", Provider: "groq", Model: "deadmodel"}}}
	c := New(cfg)
	if c == nil {
		t.Fatal("New nil")
	}

	c.healProvider("groq")

	slots := c.Slots()
	for _, s := range slots {
		if s.Model == "deadmodel" {
			t.Fatal("dead model should have been removed")
		}
	}
	if len(slots) != 1 || slots[0].Model != "goodmodel" {
		t.Fatalf("expected goodmodel replacement, got %+v", slots)
	}
}

func TestBenchmarkStoreRoundTrip(t *testing.T) {
	st, err := NewBenchmarkStore(filepath.Join(t.TempDir(), "bench.db"))
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	defer st.Close()

	// Cases seed defaults on first read.
	if got := st.Cases(); len(got) != len(DefaultBenchmarkCases) {
		t.Fatalf("expected default cases seeded, got %d", len(got))
	}
	// Editable: replace the set.
	custom := []BenchmarkCase{{Raw: "Foo.2020", Expect: "Foo"}}
	if err := st.SetCases(custom); err != nil {
		t.Fatalf("SetCases: %v", err)
	}
	if got := st.Cases(); len(got) != 1 || got[0].Expect != "Foo" {
		t.Fatalf("cases not replaced: %+v", got)
	}

	// Results + order persist best-first.
	scores := []SlotScore{
		{SlotID: "x", Composite: 0.9, Accuracy: 0.9, AvgLatencyMs: 500},
		{SlotID: "y", Composite: 0.2, Accuracy: 0.5, AvgLatencyMs: 6000},
	}
	if err := st.SaveResults(scores); err != nil {
		t.Fatalf("SaveResults: %v", err)
	}
	order := st.Order()
	if len(order) != 2 || order[0] != "x" || order[1] != "y" {
		t.Fatalf("order wrong: %v", order)
	}
	if res := st.Results(); len(res) != 2 || res[0].SlotID != "x" {
		t.Fatalf("results wrong: %+v", res)
	}
}
