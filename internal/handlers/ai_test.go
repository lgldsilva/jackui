package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/ai"
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
