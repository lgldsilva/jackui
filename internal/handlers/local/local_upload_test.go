package local

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	lb "github.com/lgldsilva/jackui/internal/local"
)

func TestLocalUpload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()

	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "Meus downloads", Path: tempDir},
	})

	router := gin.New()
	// Simple auth middleware to simulate claims and allow writing
	router.Use(func(c *gin.Context) {
		c.Set("jackui:claims", &auth.Claims{
			UserID:   1,
			Username: "testuser",
			Role:     auth.RoleAdmin,
		})
		c.Next()
	})

	router.POST("/api/local/upload", LocalUpload(b, 100<<20))

	// Prepares the multipart body
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "test.mp4")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("file content bytes"))
	_ = writer.Close()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/local/upload?mount=Meus+downloads&path=subfolder", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["uploaded"] != "test.mp4" {
		t.Errorf("uploaded=%v, want 'test.mp4'", resp["uploaded"])
	}

	// Verifies the file was successfully created on disk
	createdFile := filepath.Join(tempDir, "subfolder", "test.mp4")
	content, err := os.ReadFile(createdFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "file content bytes" {
		t.Errorf("content=%q, want 'file content bytes'", content)
	}
}

// A second upload of an existing name must auto-rename, never overwrite — one
// user clobbering another's file in a shared dir would be a data-loss bug.
func TestLocalUpload_AutoRenameOnCollision(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "movie.mkv"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := lb.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: tempDir}})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("jackui:claims", &auth.Claims{UserID: 1, Username: "u", Role: auth.RoleAdmin})
		c.Next()
	})
	router.POST("/api/local/upload", LocalUpload(b, 100<<20))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "movie.mkv")
	_, _ = part.Write([]byte("new content"))
	_ = writer.Close()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/local/upload?mount=Meus+downloads&path=", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["uploaded"] != "movie (1).mkv" {
		t.Errorf("uploaded=%v, want 'movie (1).mkv'", resp["uploaded"])
	}
	// Original must be intact.
	orig, _ := os.ReadFile(filepath.Join(tempDir, "movie.mkv"))
	if string(orig) != "original" {
		t.Errorf("original file was overwritten: %q", orig)
	}
	renamed, err := os.ReadFile(filepath.Join(tempDir, "movie (1).mkv"))
	if err != nil || string(renamed) != "new content" {
		t.Errorf("renamed file = %q, err=%v", renamed, err)
	}
}

// Non-admins may only write to "Meus downloads"; any other mount is 403.
func TestLocalUpload_ForbiddenForNonAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()

	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "HD Externo", Path: tempDir},
	})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("jackui:claims", &auth.Claims{UserID: 2, Username: "comum", Role: auth.RoleUser})
		c.Next()
	})
	router.POST("/api/local/upload", LocalUpload(b, 100<<20))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "x.txt")
	_, _ = part.Write([]byte("data"))
	_ = writer.Close()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/local/upload?mount=HD+Externo&path=", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want 403", w.Code, w.Body.String())
	}
	// Nothing should have been written to disk.
	if entries, _ := os.ReadDir(tempDir); len(entries) != 0 {
		t.Errorf("expected empty directory, found %d entries", len(entries))
	}
}

// LocalFile must neutralize the stored-XSS vector: active formats download
// instead of rendering, subtitles get text/vtt, and sniffing is always off.
func TestLocalFile_SecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "evil.html"), []byte("<script>steal()</script>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "sub.vtt"), []byte("WEBVTT\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := lb.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: tempDir}})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("jackui:claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})
		c.Next()
	})
	router.GET("/api/local/file", LocalFile(b, nil, nil))

	cases := []struct {
		name, file, wantType, wantDisp string
	}{
		{"html becomes download", "evil.html", "application/octet-stream", "attachment; filename=\"evil.html\""},
		{"vtt becomes text/vtt", "sub.vtt", "text/vtt; charset=utf-8", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/local/file?mount=Meus+downloads&path="+tc.file, nil)
			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options=%q, want nosniff", got)
			}
			if got := w.Header().Get("Content-Type"); got != tc.wantType {
				t.Errorf("Content-Type=%q, want %q", got, tc.wantType)
			}
			if got := w.Header().Get("Content-Disposition"); got != tc.wantDisp {
				t.Errorf("Content-Disposition=%q, want %q", got, tc.wantDisp)
			}
		})
	}
}

// Uploads of types outside the allowlist (e.g. .html) are blocked at the entrance with
// 415 — defense in depth beyond the serving's anti-XSS guard.
func TestLocalUpload_RejectsDisallowedType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: tempDir}})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("jackui:claims", &auth.Claims{UserID: 1, Username: "u", Role: auth.RoleAdmin})
		c.Next()
	})
	router.POST("/api/local/upload", LocalUpload(b, 100<<20))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "evil.html")
	_, _ = part.Write([]byte("<script>alert(1)</script>"))
	_ = writer.Close()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/local/upload?mount=Meus+downloads&path=", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status=%d body=%s, want 415", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(tempDir, "evil.html")); !os.IsNotExist(err) {
		t.Error("disallowed file was written to disk")
	}
}

// An upload above the ceiling is rejected (MaxBytesReader cuts the body → 400, or the
// already-reported large Size → 413). In both cases, never 201.
func TestLocalUpload_RejectsOversize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: tempDir}})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("jackui:claims", &auth.Claims{UserID: 1, Username: "u", Role: auth.RoleAdmin})
		c.Next()
	})
	router.POST("/api/local/upload", LocalUpload(b, 10)) // 10-byte ceiling

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "big.mp4")
	_, _ = part.Write(bytes.Repeat([]byte("x"), 1024)) // 1KB >> 10 bytes
	_ = writer.Close()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/local/upload?mount=Meus+downloads&path=", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(w, req)

	if w.Code == http.StatusCreated {
		t.Fatalf("large upload was accepted (status=%d); it should be rejected", w.Code)
	}
}

// Upload with an invalid destination path (traversal) → 400 (covers resolveUploadDest).
func TestLocalUpload_RejectsBadDestPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: tempDir}})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("jackui:claims", &auth.Claims{UserID: 1, Username: "u", Role: auth.RoleAdmin})
		c.Next()
	})
	router.POST("/api/local/upload", LocalUpload(b, 100<<20))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "ok.mp4")
	_, _ = part.Write([]byte("data"))
	_ = writer.Close()
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/local/upload?mount=Meus+downloads&path=../../etc", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(w, req)
	if w.Code == http.StatusCreated {
		t.Fatalf("path traversal in the destination was accepted (status=%d)", w.Code)
	}
}

// Upload without the "file" field → 400 (covers validateUpload without a file).
func TestLocalUpload_MissingFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: tempDir}})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("jackui:claims", &auth.Claims{UserID: 1, Username: "u", Role: auth.RoleAdmin})
		c.Next()
	})
	router.POST("/api/local/upload", LocalUpload(b, 100<<20))
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("nada", "x")
	_ = writer.Close()
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/local/upload?mount=Meus+downloads&path=", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without a file, got %d", w.Code)
	}
}
