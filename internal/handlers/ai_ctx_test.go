package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/dbtest"
)

func aiGoneRequest(path string) (*httptest.ResponseRecorder, *gin.Context) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, path, nil).WithContext(ctx)
	return w, c
}

func aiTestClient(t *testing.T) *ai.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"Inception\",\"year\":2010,\"kind\":\"movie\"}"}}]}`))
	}))
	t.Cleanup(srv.Close)
	client := ai.New(config.AIConfig{
		Enabled:   true,
		Providers: map[string]config.AIProvider{"groq": {BaseURL: srv.URL}},
		Chain:     []config.AIChainSlot{{ID: "groq:m", Provider: "groq", Model: "m"}},
	})
	if client == nil {
		t.Fatal("client nil")
	}
	return client
}

// A request whose client is already gone must not turn a failed read into an
// empty set that the detached write then commits over the stored results.
func TestRunAIBenchmarkIncomplete_ClientGone_KeepsPersistedResults(t *testing.T) {
	store, err := ai.NewBenchmarkStore(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	live := context.Background()
	if err := store.SaveResults(live, []ai.SlotScore{{SlotID: "groq:m", Provider: "groq", Model: "m", Incomplete: true}}); err != nil {
		t.Fatal(err)
	}
	w, c := aiGoneRequest("/api/ai/benchmark/rerun-incomplete")
	RunAIBenchmarkIncomplete(aiTestClient(t), store, nil)(c)

	if got := store.Results(live); len(got) != 1 {
		t.Fatalf("persisted results after a gone-client rerun = %d, want 1 (status %d)", len(got), w.Code)
	}
	if w.Code == http.StatusOK {
		t.Errorf("a rerun whose inputs could not be read must not report success")
	}
}

// A failed Cases read must not silently benchmark (and persist) the defaults in
// place of the user's edited set.
func TestRunAIBenchmark_ClientGone_DoesNotFallBackToDefaultCases(t *testing.T) {
	store, err := ai.NewBenchmarkStore(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	live := context.Background()
	custom := []ai.BenchmarkCase{{Raw: "Inception.2010", Expect: "Inception"}}
	if err := store.SetCases(live, custom); err != nil {
		t.Fatal(err)
	}
	w, c := aiGoneRequest("/api/ai/benchmark")
	RunAIBenchmark(aiTestClient(t), store, nil)(c)

	if w.Code == http.StatusOK {
		t.Errorf("benchmark ran although the case set could not be read")
	}
	if got := store.Results(live); len(got) != 0 {
		t.Errorf("results persisted from a run with unreadable cases: %+v", got)
	}
	if got := store.Cases(live); len(got) != 1 {
		t.Errorf("user's case set altered: %d cases", len(got))
	}
}
