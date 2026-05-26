package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/jackett"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func mockJackettClient(t *testing.T, handler http.HandlerFunc) *jackett.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return jackett.New(srv.URL, "testkey")
}

func jackettResultsJSON(results ...map[string]interface{}) string {
	payload := map[string]interface{}{"Results": results}
	b, _ := json.Marshal(payload)
	return string(b)
}

func TestSearch_MissingQuery(t *testing.T) {
	client := mockJackettClient(t, func(w http.ResponseWriter, r *http.Request) {})

	router := gin.New()
	router.GET("/api/search", Search(client))

	req := httptest.NewRequest("GET", "/api/search", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] == "" {
		t.Error("expected error message in response body")
	}
}

func TestSearch_ReturnsResults(t *testing.T) {
	client := mockJackettClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jackettResultsJSON(
			map[string]interface{}{
				"Title": "Test Torrent", "Tracker": "TestTracker",
				"Category": []int{}, "Size": 1000, "Seeders": 50, "Peers": 60,
			},
		)))
	})

	router := gin.New()
	router.GET("/api/search", Search(client))

	req := httptest.NewRequest("GET", "/api/search?q=ubuntu", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — body: %s", w.Code, w.Body.String())
	}

	var results []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0]["title"] != "Test Torrent" {
		t.Errorf("title = %v, want 'Test Torrent'", results[0]["title"])
	}
}

func TestSearch_ForwardsIndexerToJackett(t *testing.T) {
	var capturedPath string
	client := mockJackettClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Write([]byte(`{"Results":[]}`))
	})

	router := gin.New()
	router.GET("/api/search", Search(client))

	req := httptest.NewRequest("GET", "/api/search?q=test&indexers=1337x", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(capturedPath, "1337x") {
		t.Errorf("Jackett path %q should contain '1337x'", capturedPath)
	}
}

func TestSearch_ForwardsCategoryToJackett(t *testing.T) {
	var capturedCategory string
	client := mockJackettClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedCategory = r.URL.Query().Get("Category[]")
		w.Write([]byte(`{"Results":[]}`))
	})

	router := gin.New()
	router.GET("/api/search", Search(client))

	req := httptest.NewRequest("GET", "/api/search?q=test&category=2000", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if capturedCategory != "2000" {
		t.Errorf("Category[] = %q, want '2000'", capturedCategory)
	}
}

func TestSearch_JackettError_Returns502(t *testing.T) {
	client := mockJackettClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("jackett is down"))
	})

	router := gin.New()
	router.GET("/api/search", Search(client))

	req := httptest.NewRequest("GET", "/api/search?q=test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}

func TestGetIndexers_ReturnsOnlyConfigured(t *testing.T) {
	client := mockJackettClient(t, func(w http.ResponseWriter, r *http.Request) {
		indexers := []map[string]interface{}{
			{"id": "1337x", "name": "1337x", "configured": true},
			{"id": "rarbg", "name": "RARBG", "configured": false},
		}
		json.NewEncoder(w).Encode(indexers)
	})

	router := gin.New()
	router.GET("/api/indexers", GetIndexers(client))

	req := httptest.NewRequest("GET", "/api/indexers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var body []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)

	if len(body) != 1 {
		t.Errorf("len(indexers) = %d, want 1 (only configured)", len(body))
	}
	if body[0]["id"] != "1337x" {
		t.Errorf("indexer id = %v, want '1337x'", body[0]["id"])
	}
}

func TestGetIndexers_JackettError_Returns502(t *testing.T) {
	client := mockJackettClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	router := gin.New()
	router.GET("/api/indexers", GetIndexers(client))

	req := httptest.NewRequest("GET", "/api/indexers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}
