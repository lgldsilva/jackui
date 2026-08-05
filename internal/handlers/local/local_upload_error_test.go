package local

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
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
