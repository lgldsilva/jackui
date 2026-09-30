package local

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/config"
	lb "github.com/lgldsilva/jackui/internal/local"
)

// createUploadFile returns an internal error when the destination directory
// is not writable (not a name collision). Covers the RespondErrorMessage
// branch introduced for S1192.
func TestCreateUploadFile_WriteError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fileAsDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)

	_, _, ok := createUploadFile(c, filepath.Join(fileAsDir, "sub"), filepath.Join(fileAsDir, "sub", "a.txt"), "a.txt")
	if ok {
		t.Fatal("expected createUploadFile to fail")
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
}

// streamUploadToDisk returns an internal error when the destination directory
// cannot be created. Covers the RespondErrorMessage branch in streamUploadToDisk.
func TestStreamUploadToDisk_MkdirError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fileAsDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "a.txt")
	_, _ = part.Write([]byte("data"))
	_ = writer.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	fileHeader, err := c.FormFile("file")
	if err != nil {
		t.Fatalf("FormFile: %v", err)
	}

	_, ok := streamUploadToDisk(c, fileHeader, filepath.Join(fileAsDir, "sub"), filepath.Join(fileAsDir, "sub", "a.txt"), "a.txt")
	if ok {
		t.Fatal("expected streamUploadToDisk to fail")
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
}

// A FileHeader with neither in-memory content nor a temp file cannot be opened
// — the handler must answer 500 instead of panicking on a nil reader.
func TestStreamUploadToDisk_OpenError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)

	dir := t.TempDir()
	_, ok := streamUploadToDisk(c, &multipart.FileHeader{Filename: "a.txt"}, dir, filepath.Join(dir, "a.txt"), "a.txt")
	if ok {
		t.Fatal("expected streamUploadToDisk to fail")
	}
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "failed to open uploaded file") {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
}

// resolveUploadDest's last line of defence: a filename that Joins OUTSIDE the
// resolved directory (only ".." survives the base-name sanitizer upstream) is
// refused as traversal.
func TestResolveUploadDest_TraversalDetected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Test", Path: dir}})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/local/upload?mount=Test&path=", nil)

	_, _, ok := resolveUploadDest(c, b, "Test", "", "..")
	if ok {
		t.Fatal("expected traversal to be refused")
	}
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "path traversal detected") {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
}

// createUploadFile gives up after 9999 "name (N)" variants rather than looping
// forever on a directory flooded with same-named files.
func TestCreateUploadFile_TooManyCollisions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 9999; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("a (%d).txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)

	_, _, ok := createUploadFile(c, dir, filepath.Join(dir, "a.txt"), "a.txt")
	if ok {
		t.Fatal("expected createUploadFile to give up")
	}
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "too many files with the same name") {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
}
