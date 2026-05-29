package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/history"
	"github.com/luizg/jackui/internal/jackett"
)

func setAuth(c *gin.Context, userID int, isAdmin bool) {
	role := auth.RoleUser
	if isAdmin {
		role = auth.RoleAdmin
	}
	c.Set("auth.claims", &auth.Claims{UserID: userID, Username: "test", Role: role})
}

func TestGetHistory_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/history", nil)
	setAuth(c, 1, false)

	GetHistory(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var entries []history.Entry
	json.Unmarshal(w.Body.Bytes(), &entries)
	if entries == nil {
		t.Error("expected non-nil empty array")
	}
}

func TestGetHistory_WithEntries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}

	err = store.Save("test query", []jackett.Result{
		{Title: "Test Result", InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/history", nil)
	setAuth(c, 1, false)

	GetHistory(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var entries []history.Entry
	json.Unmarshal(w.Body.Bytes(), &entries)
	if len(entries) == 0 {
		t.Error("expected at least one entry")
	}
}

func TestGetHistory_AdminAll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}

	_ = store.Save("user1 query", []jackett.Result{
		{Title: "User1 Result", InfoHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	}, 1)
	_ = store.Save("user2 query", []jackett.Result{
		{Title: "User2 Result", InfoHash: "cccccccccccccccccccccccccccccccccccccccc"},
	}, 2)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/history?all=1", nil)
	setAuth(c, 2, true)

	GetHistory(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var entries []history.Entry
	json.Unmarshal(w.Body.Bytes(), &entries)
	if len(entries) < 2 {
		t.Errorf("expected at least 2 entries for admin, got %d", len(entries))
	}
}

func TestGetHistoryResults_MissingQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/history/results", nil)
	setAuth(c, 1, false)

	GetHistoryResults(store, nil, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] == "" {
		t.Error("expected error message")
	}
}

func TestGetHistoryResults_WithQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/history/results?q=test", nil)
	setAuth(c, 1, false)

	GetHistoryResults(store, nil, nil)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var results []interface{}
	json.Unmarshal(w.Body.Bytes(), &results)
	if results == nil {
		t.Error("expected non-nil array")
	}
}

func TestSearchCache_MissingQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/history/cache", nil)
	setAuth(c, 1, false)

	SearchCache(store, nil, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestSearchCache_WithQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/history/cache?q=test", nil)
	setAuth(c, 1, false)

	SearchCache(store, nil, nil)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDeleteHistory_WithoutQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Save("test", []jackett.Result{
		{Title: "Test Result", InfoHash: "dddddddddddddddddddddddddddddddddddddddd"},
	}, 1)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/history", nil)
	setAuth(c, 1, false)

	DeleteHistory(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["message"] != "history cleared" {
		t.Errorf("message = %q, want 'history cleared'", body["message"])
	}
}

func TestDeleteHistory_WithQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/history?q=test", nil)
	setAuth(c, 1, false)

	DeleteHistory(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["message"] != "query cleared" {
		t.Errorf("message = %q, want 'query cleared'", body["message"])
	}
}

func TestDeleteHistory_AdminAll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/history?all=1", nil)
	setAuth(c, 1, true)

	DeleteHistory(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
