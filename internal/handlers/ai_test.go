package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/config"
)

func TestGetAIBenchmark_NilClientNilStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/ai/benchmark", nil)

	GetAIBenchmark(nil, nil)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp aiStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal("invalid JSON:", err)
	}
	if resp.Enabled {
		t.Error("expected enabled=false")
	}
	if resp.Chain != nil {
		t.Errorf("expected nil chain, got %v", resp.Chain)
	}
}

func TestGetAIBenchmark_WithStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := ai.NewBenchmarkStore(t.TempDir() + "/ai-benchmark.db")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/ai/benchmark", nil)

	GetAIBenchmark(nil, store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp aiStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal("invalid JSON:", err)
	}
	if resp.Cases == nil {
		t.Error("expected non-nil Cases")
	}
}

func TestPutAICases_NilStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/ai/benchmark/cases", bytes.NewReader([]byte(`{"cases":[]}`)))
	c.Request.Header.Set("Content-Type", "application/json")

	PutAICases(nil)(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestPutAICases_InvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := ai.NewBenchmarkStore(t.TempDir() + "/ai-benchmark.db")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/ai/benchmark/cases", bytes.NewReader([]byte(`not json`)))
	c.Request.Header.Set("Content-Type", "application/json")

	PutAICases(store)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestPutAICases_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := ai.NewBenchmarkStore(t.TempDir() + "/ai-benchmark.db")
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]interface{}{
		"cases": []map[string]interface{}{
			{"query": "test movie 2024", "expected": "Test Movie"},
		},
	}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/ai/benchmark/cases", bytes.NewReader(b))
	c.Request.Header.Set("Content-Type", "application/json")

	PutAICases(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Cases []ai.BenchmarkCase `json:"cases"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal("invalid JSON:", err)
	}
	if len(resp.Cases) < 1 {
		t.Errorf("expected at least 1 case, got %d", len(resp.Cases))
	}
}

func TestPutAICases_StoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := ai.NewBenchmarkStore(t.TempDir() + "/ai-benchmark.db")
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/ai/benchmark/cases", bytes.NewReader([]byte(`{"cases":[]}`)))
	c.Request.Header.Set("Content-Type", "application/json")

	PutAICases(store)(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}

func TestRunAIBenchmark_NilClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/ai/benchmark", nil)

	RunAIBenchmark(nil, nil)(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestPutAICostConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := ai.New(config.AIConfig{
		Enabled:   true,
		Providers: map[string]config.AIProvider{"groq": {BaseURL: "http://x"}},
		Chain:     []config.AIChainSlot{{ID: "groq:m", Provider: "groq", Model: "m"}},
	})
	store, err := ai.NewBenchmarkStore(t.TempDir() + "/b.db")
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/ai/settings", bytes.NewReader([]byte(`{"maxCostPer1M":0.5,"kwhPrice":0.16,"localWatts":300}`)))
	PutAICostConfig(client, store)(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if cc := client.CostConfig(); cc.MaxCostPer1M != 0.5 || cc.KWhPrice != 0.16 || cc.LocalWatts != 300 {
		t.Fatalf("live cost not applied: %+v", cc)
	}
	if got, ok := store.LoadCostConfig(); !ok || got.MaxCostPer1M != 0.5 {
		t.Fatalf("not persisted: %+v ok=%v", got, ok)
	}

	// Nil client → 503.
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest("PUT", "/x", nil)
	PutAICostConfig(nil, nil)(c2)
	if w2.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil client want 503, got %d", w2.Code)
	}

	// Negative value → 400.
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Request = httptest.NewRequest("PUT", "/x", bytes.NewReader([]byte(`{"kwhPrice":-1}`)))
	PutAICostConfig(client, store)(c3)
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("negative want 400, got %d", w3.Code)
	}

	// Malformed JSON → 400.
	w4 := httptest.NewRecorder()
	c4, _ := gin.CreateTestContext(w4)
	c4.Request = httptest.NewRequest("PUT", "/x", bytes.NewReader([]byte(`not json`)))
	PutAICostConfig(client, store)(c4)
	if w4.Code != http.StatusBadRequest {
		t.Fatalf("malformed want 400, got %d", w4.Code)
	}

	// Nil store (client ok) still applies live and returns 200.
	w5 := httptest.NewRecorder()
	c5, _ := gin.CreateTestContext(w5)
	c5.Request = httptest.NewRequest("PUT", "/x", bytes.NewReader([]byte(`{"maxCostPer1M":1}`)))
	PutAICostConfig(client, nil)(c5)
	if w5.Code != http.StatusOK {
		t.Fatalf("nil store want 200, got %d", w5.Code)
	}
	if client.CostConfig().MaxCostPer1M != 1 {
		t.Fatal("nil store should still apply live")
	}
}

func TestRunAIBenchmarkIncomplete_NilClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/ai/benchmark/rerun-incomplete", nil)
	RunAIBenchmarkIncomplete(nil, nil)(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestRunAIBenchmarkIncomplete_NilStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := ai.New(config.AIConfig{
		Enabled:   true,
		Providers: map[string]config.AIProvider{"groq": {BaseURL: "http://x"}},
		Chain:     []config.AIChainSlot{{ID: "groq:m", Provider: "groq", Model: "m"}},
	})
	if client == nil {
		t.Fatal("client nil")
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/ai/benchmark/rerun-incomplete", nil)
	RunAIBenchmarkIncomplete(client, nil)(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

// TestRunAIBenchmarkIncomplete_RerunsIncomplete: a persisted Incomplete model that
// now answers is re-run, merged, and no longer incomplete.
func TestRunAIBenchmarkIncomplete_RerunsIncomplete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"Inception\",\"year\":2010,\"kind\":\"movie\"}"}}]}`))
	}))
	defer srv.Close()

	client := ai.New(config.AIConfig{
		Enabled:   true,
		Providers: map[string]config.AIProvider{"groq": {BaseURL: srv.URL}},
		Chain:     []config.AIChainSlot{{ID: "groq:m", Provider: "groq", Model: "m"}},
	})
	if client == nil {
		t.Fatal("client nil")
	}
	store, err := ai.NewBenchmarkStore(t.TempDir() + "/ai-benchmark.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveResults([]ai.SlotScore{{SlotID: "groq:m", Provider: "groq", Model: "m", Incomplete: true}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCases([]ai.BenchmarkCase{{Raw: "Inception.2010", Expect: "Inception"}}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/ai/benchmark/rerun-incomplete", nil)
	RunAIBenchmarkIncomplete(client, store)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	for _, sc := range store.Results() {
		if sc.SlotID == "groq:m" && (sc.Incomplete || sc.Accuracy != 1) {
			t.Fatalf("model should be complete + accurate after rerun, got %+v", sc)
		}
	}
}

func TestRunAIBenchmark_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"Inception\",\"year\":2010,\"kind\":\"movie\"}"}}]}`))
	}))
	defer srv.Close()

	client := ai.New(config.AIConfig{
		Enabled:   true,
		Providers: map[string]config.AIProvider{"groq": {BaseURL: srv.URL}},
		Chain:     []config.AIChainSlot{{ID: "groq:m", Provider: "groq", Model: "m"}},
	})
	if client == nil {
		t.Fatal("client nil")
	}
	store, err := ai.NewBenchmarkStore(t.TempDir() + "/ai-benchmark.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetCases([]ai.BenchmarkCase{{Raw: "Inception.2010", Expect: "Inception"}}); err != nil {
		t.Fatal(err)
	}

	// 1. Run all (no query params)
	{
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/ai/benchmark", nil)
		RunAIBenchmark(client, store)(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
		}
	}

	// 2. Run with query params provider & model
	{
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/ai/benchmark?provider=groq&model=m", nil)
		RunAIBenchmark(client, store)(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
		}
	}

	// 3. Run with query param provider (no model)
	{
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/ai/benchmark?provider=groq", nil)
		RunAIBenchmark(client, store)(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
		}
	}

	// 4. Run with query param model (no provider)
	{
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/ai/benchmark?model=m", nil)
		RunAIBenchmark(client, store)(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
		}
	}
}

// TestPersistBenchmarkRun covers the helper directly: pass-through with no store,
// and the error path when the store's DB is unusable (closed) so RecordRun fails.
func TestPersistBenchmarkRun(t *testing.T) {
	scores := []ai.SlotScore{{SlotID: "p:m", Provider: "p", Model: "m", Samples: 1}}

	// No store → pass-through, no error.
	got, err := persistBenchmarkRun(nil, scores, "", "")
	if err != nil || len(got) != 1 {
		t.Fatalf("nil store: got %v, err %v", got, err)
	}

	// Closed store → RecordRun's Begin fails → error bubbles up.
	store, err := ai.NewBenchmarkStore(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	if _, err := persistBenchmarkRun(store, scores, "", ""); err == nil {
		t.Fatal("expected an error from a closed store")
	}
}
