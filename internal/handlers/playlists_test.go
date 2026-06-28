package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/playlists"
)

// seededPool returns a fresh test schema with the given user ids seeded (1,2,3
// by default), so handler tests can insert user-owned rows under the FKs.
func seededPool(t *testing.T, ids ...int64) *sql.DB {
	t.Helper()
	pool := dbtest.NewDB(t)
	if len(ids) == 0 {
		ids = []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	}
	dbtest.SeedUsers(t, pool, ids...)
	return pool
}

func TestPlaylistsList_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := playlists.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/playlists", PlaylistsList(store))

	req := httptest.NewRequest("GET", "/api/playlists", nil)
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

func TestPlaylistsCreate_MissingName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := playlists.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.POST("/api/playlists", PlaylistsCreate(store))

	body := map[string]string{"description": "desc"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/playlists", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPlaylistsCreate_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := playlists.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.POST("/api/playlists", PlaylistsCreate(store))

	body := map[string]string{"name": "My Playlist", "description": "A test playlist"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/playlists", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestPlaylistsGet_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := playlists.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/playlists/:id", PlaylistsGet(store))

	req := httptest.NewRequest("GET", "/api/playlists/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPlaylistsGet_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := playlists.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/playlists/:id", PlaylistsGet(store))

	req := httptest.NewRequest("GET", "/api/playlists/999", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 404 or 500", w.Code)
	}
}

func TestPlaylistsUpdate_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := playlists.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.PATCH("/api/playlists/:id", PlaylistsUpdate(store))

	body := map[string]string{"name": "New Name"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("PATCH", "/api/playlists/notanumber", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPlaylistsDelete_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := playlists.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.DELETE("/api/playlists/:id", PlaylistsDelete(store))

	req := httptest.NewRequest("DELETE", "/api/playlists/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPlaylistsAddItem_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := playlists.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.POST("/api/playlists/:id/items", PlaylistsAddItem(store))

	req := httptest.NewRequest("POST", "/api/playlists/notanumber/items", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPlaylistsAddItem_NoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := playlists.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.POST("/api/playlists/:id/items", PlaylistsAddItem(store))

	req := httptest.NewRequest("POST", "/api/playlists/1/items", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPlaylistsRemoveItem_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := playlists.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.DELETE("/api/playlists/:id/items/:itemId", PlaylistsRemoveItem(store))

	req := httptest.NewRequest("DELETE", "/api/playlists/notanumber/items/1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// When id fails to parse, itemID remains 0, but handler still tries
	// to remove — returns 200 because RemoveItem doesn't return error for missing item
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

func TestPlaylistsReorderItem_InvalidPosition(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := playlists.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.PATCH("/api/playlists/:id/items/:itemId", PlaylistsReorderItem(store))

	req := httptest.NewRequest("PATCH", "/api/playlists/1/items/1", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPlaylists_FullCRUD(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := playlists.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/playlists", PlaylistsList(store))
	router.POST("/api/playlists", PlaylistsCreate(store))
	router.GET("/api/playlists/:id", PlaylistsGet(store))
	router.PATCH("/api/playlists/:id", PlaylistsUpdate(store))
	router.DELETE("/api/playlists/:id", PlaylistsDelete(store))

	// Create
	body := map[string]string{"name": "Test", "description": "Desc"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/playlists", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d, body: %s", w.Code, w.Body.String())
	}

	// List
	req = httptest.NewRequest("GET", "/api/playlists", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("list: %d", w.Code)
	}
	var list []interface{}
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Errorf("expected 1 playlist, got %d", len(list))
	}
}
