package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/history"
	"github.com/luizg/jackui/internal/jackett"
)

func TestHealth_NilStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/healthz", nil)

	Health(nil)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("status = %v, want 'ok'", body["status"])
	}
	if body["db"] != "disabled" {
		t.Errorf("db = %v, want 'disabled'", body["db"])
	}
	if _, ok := body["time"]; !ok {
		t.Error("expected time field")
	}
}

func TestHealth_WithStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/healthz", nil)

	Health(db)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("status = %v, want 'ok'", body["status"])
	}
	if body["db"] != "ok" {
		t.Errorf("db = %v, want 'ok'", body["db"])
	}
}

func TestStatus_NilStoreAndNoJackett(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	client := jackett.New(srv.URL, "testkey")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/status", nil)

	Status(client, nil)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["db"] != "disabled" {
		t.Errorf("db = %v, want 'disabled'", body["db"])
	}
}

func TestHealth_JSONStructure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/healthz", nil)

	Health(nil)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal("invalid JSON:", err)
	}
	if _, ok := resp["status"]; !ok {
		t.Error("missing status field")
	}
	if _, ok := resp["db"]; !ok {
		t.Error("missing db field")
	}
	if _, ok := resp["time"]; !ok {
		t.Error("missing time field")
	}
}

func TestStatus_DbDegraded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	client := jackett.New(srv.URL, "testkey")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/status", nil)

	Status(client, store)(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "degraded" {
		t.Errorf("status = %v, want 'degraded'", body["status"])
	}
	if !strings.HasPrefix(body["db"].(string), "down:") {
		t.Errorf("db = %v, want 'down:...'", body["db"])
	}
}

func TestStatus_Healthy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	client := jackett.New(srv.URL, "testkey")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/status", nil)

	Status(client, store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("status = %v, want 'ok'", body["status"])
	}
	if body["db"] != "ok" {
		t.Errorf("db = %v, want 'ok'", body["db"])
	}
}
