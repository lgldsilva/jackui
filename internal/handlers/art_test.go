package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestStreamArt_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/art/:hash", StreamArt(s))

	req := httptest.NewRequest("GET", "/api/stream/art/nothex", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamArt_NoArt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/art/:hash", StreamArt(s))

	req := httptest.NewRequest("GET", "/api/stream/art/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
}

func TestStreamArt_ShortHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/art/:hash", StreamArt(s))

	req := httptest.NewRequest("GET", "/api/stream/art/short", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestFoldersList_StoreUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/favorites/folders", FoldersList(s))

	req := httptest.NewRequest("GET", "/api/stream/favorites/folders", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestFolderCreate_NoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/favorites/folders", FolderCreate(s))

	req := httptest.NewRequest("POST", "/api/stream/favorites/folders", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestFolderDelete_BadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.DELETE("/api/stream/favorites/folders/:id", FolderDelete(s))

	req := httptest.NewRequest("DELETE", "/api/stream/favorites/folders/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}
