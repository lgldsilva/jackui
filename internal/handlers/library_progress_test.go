package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/library"
)

// PUT /api/library/progress (issue #201): native clients save playback
// progress by infoHash — no library ID, upsert semantics, watched ≥ 90%.

func progressRouter(t *testing.T) (*gin.Engine, *library.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	lib, err := library.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.PUT("/api/library/progress", func(c *gin.Context) { setAuth(c, 1, false) }, LibraryProgress(lib))
	return router, lib
}

func progressDo(router *gin.Engine, body map[string]any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/library/progress", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestLibraryProgress_SavesByHash(t *testing.T) {
	router, lib := progressRouter(t)

	w := progressDo(router, map[string]any{"infoHash": "abcdefgh", "fileIndex": 2, "position": 900, "duration": 1000})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	entry, err := lib.GetByHash(context.Background(), 1, "abcdefgh")
	if err != nil || entry == nil {
		t.Fatalf("lookup: %v %v", entry, err)
	}
	if entry.ResumeSeconds != 900 || entry.LastFileIndex != 2 {
		t.Errorf("resume=%v file=%d, want 900/2", entry.ResumeSeconds, entry.LastFileIndex)
	}
}

func TestLibraryProgress_RejectsEmptyHash(t *testing.T) {
	router, _ := progressRouter(t)
	w := progressDo(router, map[string]any{"position": 1, "duration": 10})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "bad_request" {
		t.Errorf("code = %v, want bad_request", body["code"])
	}
}

func TestLibraryProgress_RejectsBadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib, err := library.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.PUT("/api/library/progress", func(c *gin.Context) { setAuth(c, 1, false) }, LibraryProgress(lib))
	req := httptest.NewRequest(http.MethodPut, "/api/library/progress", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
