package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
)

func TestHealth_NilStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/healthz", nil)

	Health(nil, nil)(c)

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

	Health(db, func() bool { return true })(c)

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

// A live DB but a streamer that failed to init → 503 (degraded), so the Docker
// healthcheck catches a process running without streaming.
func TestHealth_StreamerDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/healthz", nil)

	Health(db, func() bool { return false })(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "degraded" {
		t.Errorf("status = %v, want 'degraded'", body["status"])
	}
	if body["streamer"] != "down" {
		t.Errorf("streamer = %v, want 'down'", body["streamer"])
	}
	if body["db"] != "ok" {
		t.Errorf("db = %v, want 'ok' (DB is fine, only streamer is down)", body["db"])
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

	Health(nil, nil)(c)

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

func TestBuildInfo_NilStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/status", nil)

	BuildInfo(nil)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal("invalid JSON:", err)
	}
	if body["db"] != "disabled" {
		t.Errorf("db = %v, want 'disabled'", body["db"])
	}
	// Build metadata fields must always be present (defaults when not injected).
	for _, k := range []string{"version", "commit", "buildTime", "goVersion", "time"} {
		if _, ok := body[k]; !ok {
			t.Errorf("missing field %q", k)
		}
	}
}

func TestBuildInfo_WithStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/status", nil)

	BuildInfo(db)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["db"] != "ok" {
		t.Errorf("db = %v, want 'ok'", body["db"])
	}
	if body["goVersion"] == "" {
		t.Error("goVersion should be populated from runtime")
	}
}

func TestBuildInfo_DbDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	store.Close() // closed store → DB probe fails, but the endpoint stays 200.

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/status", nil)

	BuildInfo(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (build info is informational)", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
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
