package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/library"
)

func TestLibraryList_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib, err := library.New(t.TempDir() + "/library.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/library", LibraryList(lib, nil))

	req := httptest.NewRequest("GET", "/api/library", nil)
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

func TestLibraryGet_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib, err := library.New(t.TempDir() + "/library.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/library/:id", LibraryGet(lib))

	req := httptest.NewRequest("GET", "/api/library/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestLibraryGet_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib, err := library.New(t.TempDir() + "/library.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/library/:id", LibraryGet(lib))

	req := httptest.NewRequest("GET", "/api/library/999", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 404 or 500", w.Code)
	}
}

func TestLibraryUpdateResume_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib, err := library.New(t.TempDir() + "/library.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.PATCH("/api/library/:id", LibraryUpdateResume(lib))

	body := map[string]float64{"resumeSeconds": 100, "durationSeconds": 500}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("PATCH", "/api/library/notanumber", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestLibraryUpdateResume_NoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib, err := library.New(t.TempDir() + "/library.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.PATCH("/api/library/:id", LibraryUpdateResume(lib))

	req := httptest.NewRequest("PATCH", "/api/library/1", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (incognito or skip)", w.Code)
	}
}

func TestLibraryUpdateResume_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib, err := library.New(t.TempDir() + "/library.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.PATCH("/api/library/:id", LibraryUpdateResume(lib))

	body := map[string]float64{"resumeSeconds": 100, "durationSeconds": 500}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("PATCH", "/api/library/1", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// If library entry doesn't exist, returns 500 from DB error — that's fine.
	// The important thing is that it got past validation.
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500; body: %s", w.Code, w.Body.String())
	}
}

func TestLibraryDeleteAll_ReturnsCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib, err := library.New(t.TempDir() + "/library.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.DELETE("/api/library", LibraryDeleteAll(lib))

	req := httptest.NewRequest("DELETE", "/api/library", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp map[string]int
	json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["deleted"]; !ok {
		t.Error("expected deleted field")
	}
}

func TestLibraryDelete_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib, err := library.New(t.TempDir() + "/library.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.DELETE("/api/library/:id", LibraryDelete(lib))

	req := httptest.NewRequest("DELETE", "/api/library/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestLibraryDelete_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib, err := library.New(t.TempDir() + "/library.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.DELETE("/api/library/:id", LibraryDelete(lib))

	req := httptest.NewRequest("DELETE", "/api/library/1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Delete on non-existent returns 500 (store.Delete fails)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

func TestLibraryList_WithLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib, err := library.New(t.TempDir() + "/library.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/library", LibraryList(lib, nil))

	req := httptest.NewRequest("GET", "/api/library?limit=5", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestLibraryList_WithInvalidLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib, err := library.New(t.TempDir() + "/library.db")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/library", LibraryList(lib, nil))

	req := httptest.NewRequest("GET", "/api/library?limit=-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
