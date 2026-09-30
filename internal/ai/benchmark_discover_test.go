package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

// Model discovery is best-effort: a non-200 from the provider yields no models
// (and the response body is closed), never an error that aborts the benchmark.
func TestListModels_Non200ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"down"}`))
	}))
	defer srv.Close()
	c := &Client{http: &http.Client{}}

	if got := c.listOllamaModels(context.Background(), srv.URL+"/v1"); got != nil {
		t.Errorf("listOllamaModels on 503 = %v, want nil", got)
	}
	if got := c.listOpenAIModels(context.Background(), srv.URL, "key"); got != nil {
		t.Errorf("listOpenAIModels on 503 = %v, want nil", got)
	}
}

// RunSlotsProgress fans cloud slots out in parallel and drains local slots
// sequentially; every slot reaches onResult exactly once (possibly from
// concurrent goroutines) and the returned slice is ranked.
func TestRunSlotsProgress_MixedSlotsEmitEachOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"Inception\",\"year\":2010,\"kind\":\"movie\"}"}}]}`))
	}))
	defer srv.Close()
	c := &Client{http: &http.Client{}, providers: map[string]config.AIProvider{
		"groq":   {BaseURL: srv.URL},
		"ollama": {BaseURL: srv.URL},
	}}
	slots := []Slot{
		{ID: "groq:a", Provider: "groq", Model: "a", BaseURL: srv.URL},
		{ID: "ollama:l1", Provider: "ollama", Model: "l1", BaseURL: srv.URL, Local: true},
		{ID: "groq:b", Provider: "groq", Model: "b", BaseURL: srv.URL},
		{ID: "ollama:l2", Provider: "ollama", Model: "l2", BaseURL: srv.URL, Local: true},
	}
	cases := []BenchmarkCase{{Raw: "Inception.2010", Expect: "Inception"}}

	var mu sync.Mutex
	seen := map[string]int{}
	scores := c.RunSlotsProgress(context.Background(), slots, cases, func(s SlotScore) {
		mu.Lock()
		seen[s.SlotID]++
		mu.Unlock()
	})

	if len(scores) != len(slots) {
		t.Fatalf("scores = %d, want %d", len(scores), len(slots))
	}
	for _, s := range slots {
		if seen[s.ID] != 1 {
			t.Errorf("onResult(%s) called %d times, want 1", s.ID, seen[s.ID])
		}
	}
	for _, sc := range scores {
		if sc.Accuracy != 1 {
			t.Errorf("%s accuracy = %v, want 1 (all cases matched)", sc.SlotID, sc.Accuracy)
		}
	}
	if countLocalSlots(slots) != 2 {
		t.Errorf("countLocalSlots = %d, want 2", countLocalSlots(slots))
	}
}
