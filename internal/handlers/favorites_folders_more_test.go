package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFoldersList_WithStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)

	router := gin.New()
	router.GET("/api/stream/favorites/folders", FoldersList(s))

	req := httptest.NewRequest("GET", "/api/stream/favorites/folders", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var folders []interface{}
	json.Unmarshal(w.Body.Bytes(), &folders)
	if folders == nil {
		t.Error("expected non-nil empty array")
	}
}

func TestFolderCreate_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)

	router := gin.New()
	router.POST("/api/stream/favorites/folders", FolderCreate(s))

	body := []byte(`{"name":"New Folder"}`)
	req := httptest.NewRequest("POST", "/api/stream/favorites/folders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var folder map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &folder)
	if folder["name"] != "New Folder" {
		t.Errorf("name = %v, want 'New Folder'", folder["name"])
	}
}

func TestFolderCreate_WithParentID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	fs := s.Favorites()
	parent, err := fs.CreateFolder(0, "Parent", nil, false)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.POST("/api/stream/favorites/folders", FolderCreate(s))

	body := []byte(`{"name":"Child","parentId":` + strconv.Itoa(parent.ID) + `}`)
	req := httptest.NewRequest("POST", "/api/stream/favorites/folders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
}

func TestFolderCreate_EmptyName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)

	router := gin.New()
	router.POST("/api/stream/favorites/folders", FolderCreate(s))

	body := []byte(`{"name":""}`)
	req := httptest.NewRequest("POST", "/api/stream/favorites/folders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestFolderPatch_ValidRename(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	fs := s.Favorites()
	f, err := fs.CreateFolder(0, "ToRename", nil, false)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.PATCH("/api/stream/favorites/folders/:id", FolderPatch(s))

	body := []byte(`{"name":"Renamed"}`)
	req := httptest.NewRequest("PATCH", "/api/stream/favorites/folders/"+strconv.Itoa(f.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}

func TestFolderPatch_MoveToRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	fs := s.Favorites()
	parent, _ := fs.CreateFolder(0, "Parent", nil, false)
	child, _ := fs.CreateFolder(0, "Child", &parent.ID, false)

	router := gin.New()
	router.PATCH("/api/stream/favorites/folders/:id", FolderPatch(s))

	body := []byte(`{"parentToRoot":true}`)
	req := httptest.NewRequest("PATCH", "/api/stream/favorites/folders/"+strconv.Itoa(child.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}

func TestFolderPatch_MoveToParent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	fs := s.Favorites()
	p1, _ := fs.CreateFolder(0, "Parent1", nil, false)
	p2, _ := fs.CreateFolder(0, "Parent2", nil, false)
	child, _ := fs.CreateFolder(0, "Child", &p1.ID, false)

	router := gin.New()
	router.PATCH("/api/stream/favorites/folders/:id", FolderPatch(s))

	body := []byte(`{"parentId":` + strconv.Itoa(p2.ID) + `}`)
	req := httptest.NewRequest("PATCH", "/api/stream/favorites/folders/"+strconv.Itoa(child.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}

func TestFolderDelete_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	fs := s.Favorites()
	f, err := fs.CreateFolder(0, "ToDelete", nil, false)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.DELETE("/api/stream/favorites/folders/:id", FolderDelete(s))

	req := httptest.NewRequest("DELETE", "/api/stream/favorites/folders/"+strconv.Itoa(f.ID), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}

func TestFavoriteMoveToFolder_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	fs := s.Favorites()
	f, err := fs.CreateFolder(0, "Target", nil, false)
	if err != nil {
		t.Fatal(err)
	}

	err = fs.Add("Test", "testhash", "magnet:?xt=urn:btih:testhash", "search", 0)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.PUT("/api/stream/favorites/:name/move", FavoriteMoveToFolder(s))

	body := []byte(`{"folderId":` + strconv.Itoa(f.ID) + `}`)
	req := httptest.NewRequest("PUT", "/api/stream/favorites/testhash/move", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}

func TestFavoriteMoveToFolder_ToRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	fs := s.Favorites()
	f, _ := fs.CreateFolder(0, "Folder", nil, false)
	fs.Add("Test2", "testhash2", "magnet:?xt=urn:btih:testhash2", "", 0)
	fs.MoveFavoriteToFolder(0, "testhash2", &f.ID)

	router := gin.New()
	router.PUT("/api/stream/favorites/:name/move", FavoriteMoveToFolder(s))

	body := []byte(`{"toRoot":true}`)
	req := httptest.NewRequest("PUT", "/api/stream/favorites/testhash2/move", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}

func TestApplyFolderPatch_RenameAndMove(t *testing.T) {
	s := newStreamerWithFavs(t)
	fs := s.Favorites()
	f, _ := fs.CreateFolder(0, "Original", nil, false)
	p, _ := fs.CreateFolder(0, "NewParent", nil, false)

	newName := "Renamed"
	err := applyFolderPatch(fs, 0, f.ID, &folderPatchBody{
		Name:     &newName,
		ParentID: &p.ID,
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplyFolderPatch_OnlyMove(t *testing.T) {
	s := newStreamerWithFavs(t)
	fs := s.Favorites()
	f, _ := fs.CreateFolder(0, "Movable", nil, false)
	p, _ := fs.CreateFolder(0, "Dest", nil, false)

	err := applyFolderPatch(fs, 0, f.ID, &folderPatchBody{
		ParentID: &p.ID,
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFoldersList_StoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	// Close the store to cause an error
	s.Favorites().Close()

	router := gin.New()
	router.GET("/api/stream/favorites/folders", FoldersList(s))

	req := httptest.NewRequest("GET", "/api/stream/favorites/folders", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}

func TestFolderCreate_StoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	s.Favorites().Close()

	router := gin.New()
	router.POST("/api/stream/favorites/folders", FolderCreate(s))

	body := []byte(`{"name":"Test"}`)
	req := httptest.NewRequest("POST", "/api/stream/favorites/folders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}

func TestFolderDelete_StoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newStreamerWithFavs(t)
	s.Favorites().Close()

	router := gin.New()
	router.DELETE("/api/stream/favorites/folders/:id", FolderDelete(s))

	req := httptest.NewRequest("DELETE", "/api/stream/favorites/folders/1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}
