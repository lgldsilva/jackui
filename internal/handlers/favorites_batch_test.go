package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestFavoritesBatchRemove_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	router := gin.New()
	router.POST("/api/stream/favorites/batch/remove", FavoritesBatchRemove(s))

	req := httptest.NewRequest("POST", "/api/stream/favorites/batch/remove", bytes.NewReader([]byte(`{"names":[]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestFavoritesBatchRemove_TooMany(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	router := gin.New()
	router.POST("/api/stream/favorites/batch/remove", FavoritesBatchRemove(s))

	names := make([]string, favoritesBatchMax+1)
	for i := range names {
		names[i] = "n" + strconv.Itoa(i)
	}
	body, _ := json.Marshal(map[string]any{"names": names})
	req := httptest.NewRequest("POST", "/api/stream/favorites/batch/remove", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413; body: %s", w.Code, w.Body.String())
	}
}

func TestFavoritesBatchRemove_NilStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/stream/favorites/batch/remove", FavoritesBatchRemove(s))

	req := httptest.NewRequest("POST", "/api/stream/favorites/batch/remove", bytes.NewReader([]byte(`{"names":["a"]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestFavoritesBatchRemove_SuccessAndPartial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	fs := s.Favorites()
	if err := fs.Add("Alpha", "hasha", "magnet:?xt=urn:btih:hasha", "manual", 0); err != nil {
		t.Fatal(err)
	}
	if err := fs.Add("Beta", "hashb", "magnet:?xt=urn:btih:hashb", "manual", 0); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.POST("/api/stream/favorites/batch/remove", FavoritesBatchRemove(s))

	// Empty name fails; Alpha/Beta succeed; Alpha dupe is collapsed.
	body := []byte(`{"names":["Alpha","", "Beta","Alpha"]}`)
	req := httptest.NewRequest("POST", "/api/stream/favorites/batch/remove", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Affected int      `json:"affected"`
		Total    int      `json:"total"`
		Failed   []string `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 4 {
		t.Errorf("total = %d, want 4", resp.Total)
	}
	if resp.Affected != 2 {
		t.Errorf("affected = %d, want 2", resp.Affected)
	}
	if len(resp.Failed) != 1 || resp.Failed[0] != "" {
		t.Errorf("failed = %#v, want one empty name", resp.Failed)
	}
	if fs.IsFavorite("Alpha") || fs.IsFavorite("Beta") {
		t.Error("Alpha/Beta should have been removed")
	}
}

func TestFavoritesBatchSetFolder_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	router := gin.New()
	router.POST("/api/stream/favorites/batch/folder", FavoritesBatchSetFolder(s))

	req := httptest.NewRequest("POST", "/api/stream/favorites/batch/folder", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestFavoritesBatchSetFolder_TooMany(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	router := gin.New()
	router.POST("/api/stream/favorites/batch/folder", FavoritesBatchSetFolder(s))

	names := make([]string, favoritesBatchMax+1)
	for i := range names {
		names[i] = "n" + strconv.Itoa(i)
	}
	body, _ := json.Marshal(map[string]any{"names": names, "toRoot": true})
	req := httptest.NewRequest("POST", "/api/stream/favorites/batch/folder", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413; body: %s", w.Code, w.Body.String())
	}
}

func TestFavoritesBatchSetFolder_SuccessToFolderAndRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	fs := s.Favorites()
	folder, err := fs.CreateFolder(0, "Dest", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"One", "Two", "Three"} {
		if err := fs.Add(name, strings.ToLower(name), "magnet:?xt=urn:btih:"+strings.ToLower(name), "manual", 0); err != nil {
			t.Fatal(err)
		}
	}

	router := gin.New()
	router.POST("/api/stream/favorites/batch/folder", FavoritesBatchSetFolder(s))

	// Move One + Two into folder (with empty-name failure + dupe collapse).
	body := []byte(`{"names":["One","","Two","One"],"folderId":` + strconv.Itoa(folder.ID) + `}`)
	req := httptest.NewRequest("POST", "/api/stream/favorites/batch/folder", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Affected int      `json:"affected"`
		Total    int      `json:"total"`
		Failed   []string `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Affected != 2 || resp.Total != 4 || len(resp.Failed) != 1 {
		t.Errorf("resp = %+v, want affected=2 total=4 failed=1", resp)
	}

	// Move One back to root via toRoot.
	body = []byte(`{"names":["One"],"toRoot":true}`)
	req = httptest.NewRequest("POST", "/api/stream/favorites/batch/folder", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("toRoot status = %d; body: %s", w.Code, w.Body.String())
	}

	list, err := fs.List(0, false, true)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*int{}
	for i := range list {
		byName[list[i].Name] = list[i].FolderID
	}
	if byName["One"] != nil {
		t.Errorf("One folderId = %v, want root (nil)", byName["One"])
	}
	if byName["Two"] == nil || *byName["Two"] != folder.ID {
		t.Errorf("Two folderId = %v, want %d", byName["Two"], folder.ID)
	}
}

func TestFavoritesBatchSetFolder_NilStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/stream/favorites/batch/folder", FavoritesBatchSetFolder(s))

	req := httptest.NewRequest("POST", "/api/stream/favorites/batch/folder", bytes.NewReader([]byte(`{"names":["a"],"toRoot":true}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}
