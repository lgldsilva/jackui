package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/watchlist"
)

func TestWatchlistList_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := watchlist.New(t.TempDir() + "/watchlist.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/watchlists", WatchlistList(s))

	req := httptest.NewRequest("GET", "/api/watchlists", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var list []interface{}
	json.Unmarshal(w.Body.Bytes(), &list)
	if list == nil {
		t.Error("expected non-nil empty array")
	}
}

func TestWatchlistCreate_NoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := watchlist.New(t.TempDir() + "/watchlist.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.POST("/api/watchlists", WatchlistCreate(s, nil))

	w := postJSON(t, router, "/api/watchlists", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestWatchlistCreate_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := watchlist.New(t.TempDir() + "/watchlist.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.POST("/api/watchlists", WatchlistCreate(s, nil))

	body := map[string]interface{}{
		"query":      "test show",
		"category":   "tv",
		"minSeeders": 5,
	}
	w := postJSON(t, router, "/api/watchlists", body)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestWatchlistUpdate_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := watchlist.New(t.TempDir() + "/watchlist.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.PUT("/api/watchlists/:id", WatchlistUpdate(s))

	req := httptest.NewRequest("PUT", "/api/watchlists/notanumber", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestWatchlistDelete_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := watchlist.New(t.TempDir() + "/watchlist.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.DELETE("/api/watchlists/:id", WatchlistDelete(s))

	req := httptest.NewRequest("DELETE", "/api/watchlists/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestWatchlistHits_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := watchlist.New(t.TempDir() + "/watchlist.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/watchlists/:id/hits", WatchlistHits(s))

	req := httptest.NewRequest("GET", "/api/watchlists/notanumber/hits", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestWatchlistHits_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := watchlist.New(t.TempDir() + "/watchlist.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/watchlists/:id/hits", WatchlistHits(s))

	req := httptest.NewRequest("GET", "/api/watchlists/999/hits", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound && w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 or 404; body: %s", w.Code, w.Body.String())
	}
}

func TestWatchlistHits_WithLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := watchlist.New(t.TempDir() + "/watchlist.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/watchlists/:id/hits", WatchlistHits(s))

	req := httptest.NewRequest("GET", "/api/watchlists/999/hits?limit=10", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound && w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 or 404; body: %s", w.Code, w.Body.String())
	}
}

func TestWatchlist_FullCRUD(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := watchlist.New(t.TempDir() + "/watchlist.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/watchlists", WatchlistList(s))
	router.POST("/api/watchlists", WatchlistCreate(s, nil))
	router.PUT("/api/watchlists/:id", WatchlistUpdate(s))
	router.DELETE("/api/watchlists/:id", WatchlistDelete(s))

	// Create
	w := postJSON(t, router, "/api/watchlists", map[string]interface{}{
		"query": "test show",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d, body: %s", w.Code, w.Body.String())
	}

	// List
	req := httptest.NewRequest("GET", "/api/watchlists", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("list: %d", w.Code)
	}
}

// recordingKicker captures the IDs whose immediate check was requested.
type recordingKicker struct{ ids []int }

func (k *recordingKicker) Kick(id int) { k.ids = append(k.ids, id) }

func TestWatchlistCreate_KicksImmediateCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := watchlist.New(t.TempDir() + "/watchlist.db")
	if err != nil {
		t.Fatal(err)
	}

	kicker := &recordingKicker{}
	router := gin.New()
	router.POST("/api/watchlists", WatchlistCreate(s, kicker))

	w := postJSON(t, router, "/api/watchlists", map[string]interface{}{
		"query":       "the office",
		"schedKind":   "daily",
		"schedHour":   8,
		"schedMinute": 30,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var created watchlist.Watchlist
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Kind != watchlist.SchedDaily || created.Hour != 8 || created.Minute != 30 {
		t.Fatalf("schedule not persisted: %+v", created.Schedule)
	}
	if created.NextCheckAt.IsZero() {
		t.Fatal("nextCheckAt missing in response")
	}
	if len(kicker.ids) != 1 || kicker.ids[0] != created.ID {
		t.Fatalf("kicker.ids = %v, want [%d]", kicker.ids, created.ID)
	}
}

func TestWatchlistUpdate_PersistsSchedule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := watchlist.New(t.TempDir() + "/watchlist.db")
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.Create(0, "q", "", 1, "", watchlist.Schedule{})
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.PUT("/api/watchlists/:id", WatchlistUpdate(s))

	req := httptest.NewRequest("PUT", "/api/watchlists/1", bytes.NewReader([]byte(
		`{"query":"q","schedKind":"weekly","schedWeekday":6,"schedHour":9}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	got, err := s.Get(0, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != watchlist.SchedWeekly || got.Weekday != 6 || got.Hour != 9 {
		t.Fatalf("schedule not updated: %+v", got.Schedule)
	}
}
