package handlers

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
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/config"
	"github.com/luizg/jackui/internal/local"
)

func TestLocalUpload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()

	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Meus downloads", Path: tempDir},
	})

	router := gin.New()
	// Middleware simples de autenticação para simular claims e permitir gravação
	router.Use(func(c *gin.Context) {
		c.Set("jackui:claims", &auth.Claims{
			UserID:   1,
			Username: "testuser",
			Role:     auth.RoleAdmin,
		})
		c.Next()
	})

	router.POST("/api/local/upload", LocalUpload(b))

	// Prepara multipart body
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "teste.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("conteudo do arquivo"))
	_ = writer.Close()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/local/upload?mount=Meus+downloads&path=subpasta", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["uploaded"] != "teste.txt" {
		t.Errorf("uploaded=%v, want 'teste.txt'", resp["uploaded"])
	}

	// Verifica se o arquivo foi criado com sucesso no disco
	createdFile := filepath.Join(tempDir, "subpasta", "teste.txt")
	content, err := os.ReadFile(createdFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "conteudo do arquivo" {
		t.Errorf("content=%q, want 'conteudo do arquivo'", content)
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

	b := local.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: tempDir}})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("jackui:claims", &auth.Claims{UserID: 1, Username: "u", Role: auth.RoleAdmin})
		c.Next()
	})
	router.POST("/api/local/upload", LocalUpload(b))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "movie.mkv")
	_, _ = part.Write([]byte("novo conteudo"))
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
		t.Errorf("arquivo original foi sobrescrito: %q", orig)
	}
	renamed, err := os.ReadFile(filepath.Join(tempDir, "movie (1).mkv"))
	if err != nil || string(renamed) != "novo conteudo" {
		t.Errorf("renamed file = %q, err=%v", renamed, err)
	}
}

// Non-admins may only write to "Meus downloads"; any other mount is 403.
func TestLocalUpload_ForbiddenForNonAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()

	b := local.NewBrowser([]config.ExternalMount{
		{Name: "HD Externo", Path: tempDir},
	})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("jackui:claims", &auth.Claims{UserID: 2, Username: "comum", Role: auth.RoleUser})
		c.Next()
	})
	router.POST("/api/local/upload", LocalUpload(b))

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
	// Nada deve ter sido gravado no disco.
	if entries, _ := os.ReadDir(tempDir); len(entries) != 0 {
		t.Errorf("esperava diretório vazio, achei %d entradas", len(entries))
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

	b := local.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: tempDir}})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("jackui:claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})
		c.Next()
	})
	router.GET("/api/local/file", LocalFile(b))

	cases := []struct {
		name, file, wantType, wantDisp string
	}{
		{"html vira download", "evil.html", "application/octet-stream", "attachment; filename=\"evil.html\""},
		{"vtt vira text/vtt", "sub.vtt", "text/vtt; charset=utf-8", ""},
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
