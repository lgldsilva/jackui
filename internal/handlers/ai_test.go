package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/ai"
	"github.com/luizg/jackui/internal/config"
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
