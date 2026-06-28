package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func newStreamerWithFavs(t *testing.T) *streamer.Streamer {
	t.Helper()
	s := streamer.NewForTesting()
	favStore, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	s.SetFavorites(favStore)
	return s
}

func TestFoldersList_NilFavorites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/favorites/folders", FoldersList(s))

	req := httptest.NewRequest("GET", "/api/stream/favorites/folders", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestFolderCreate_NilFavorites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/favorites/folders", FolderCreate(s))

	body := []byte(`{"name":"Test Folder"}`)
	req := httptest.NewRequest("POST", "/api/stream/favorites/folders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestFolderCreate_NoName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)

	router := gin.New()
	router.POST("/api/stream/favorites/folders", FolderCreate(s))

	body := []byte(`{}`)
	req := httptest.NewRequest("POST", "/api/stream/favorites/folders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestFolderPatch_NotANumber(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)

	router := gin.New()
	router.PATCH("/api/stream/favorites/folders/:id", FolderPatch(s))

	body := []byte(`{"name":"Renamed"}`)
	req := httptest.NewRequest("PATCH", "/api/stream/favorites/folders/abc", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestFolderPatch_InvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)

	router := gin.New()
	router.PATCH("/api/stream/favorites/folders/:id", FolderPatch(s))

	req := httptest.NewRequest("PATCH", "/api/stream/favorites/folders/1", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestFolderDelete_NotANumber(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)

	router := gin.New()
	router.DELETE("/api/stream/favorites/folders/:id", FolderDelete(s))

	req := httptest.NewRequest("DELETE", "/api/stream/favorites/folders/abc", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestFolderDelete_NilFavorites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.DELETE("/api/stream/favorites/folders/:id", FolderDelete(s))

	req := httptest.NewRequest("DELETE", "/api/stream/favorites/folders/1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestFavoriteMoveToFolder_NilFavorites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.PUT("/api/stream/favorites/:name/move", FavoriteMoveToFolder(s))

	req := httptest.NewRequest("PUT", "/api/stream/favorites/testhash/move", bytes.NewReader([]byte(`{"toRoot":true}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestFavoriteMoveToFolder_InvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)

	router := gin.New()
	router.PUT("/api/stream/favorites/:name/move", FavoriteMoveToFolder(s))

	req := httptest.NewRequest("PUT", "/api/stream/favorites/testhash/move", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestApplyFolderPatch_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	fs := s.Favorites()

	err := applyFolderPatch(fs, 999, 123, &folderPatchBody{Name: strPtr("Renamed")})
	// This should not panic — either returns nil (no-op for missing folder) or error
	_ = err
}

func strPtr(s string) *string { return &s }
