package local

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/localstream"
)

func transferBrowser(t *testing.T) (*lb.Browser, string) {
	t.Helper()
	dir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Test", Path: dir}})
	return b, dir
}

func TestLocalTransferStatus_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := transferBrowser(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/transfer-status?mount=Test", nil)

	LocalTransferStatus(b, localstream.NewRegistry(0))(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestLocalTransferStatus_ForbiddenMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := transferBrowser(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/transfer-status?mount=Nope&path=x.mkv", nil)

	LocalTransferStatus(b, localstream.NewRegistry(0))(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestLocalTransferStatus_NilRegistryInactive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := transferBrowser(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/transfer-status?mount=Test&path=x.mkv", nil)

	LocalTransferStatus(b, nil)(c)
	assertInactive(t, w)
}

func TestLocalTransferStatus_NoSessionInactive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := transferBrowser(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/transfer-status?mount=Test&path=x.mkv", nil)

	LocalTransferStatus(b, localstream.NewRegistry(0))(c)
	assertInactive(t, w)
}

func TestLocalTransferStatus_ReportsActiveSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := transferBrowser(t)
	reg := localstream.NewRegistry(0)
	defer reg.Close()

	// Stand up a live direct-play session and pull some bytes through it.
	abs := filepath.Join(dir, "movie.mp4")
	if err := os.WriteFile(abs, bytes.Repeat([]byte("x"), 4096), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := os.Open(abs)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	key := transferKeyDirect("Test", "movie.mp4")
	sess := reg.OpenSolo(key, f, 4096)
	if _, err := sess.Read(make([]byte, 2048)); err != nil {
		t.Fatalf("read: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/transfer-status?mount=Test&path=movie.mp4", nil)
	LocalTransferStatus(b, reg)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	var snap localstream.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, w.Body.String())
	}
	if !snap.Active {
		t.Fatalf("expected active session, got %+v", snap)
	}
	// The 4 KiB file is far smaller than the read-ahead window, so the first
	// Read pulls the whole file from disk in one aligned fetch — BytesRead
	// reflects underlying I/O (4096), not the 2048 the consumer asked for.
	if snap.BytesRead != 4096 {
		t.Fatalf("BytesRead=%d want 4096 (read-ahead pulled the whole file)", snap.BytesRead)
	}
	if snap.Size != 4096 {
		t.Fatalf("Size=%d want 4096", snap.Size)
	}
}

func assertInactive(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	var snap localstream.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, w.Body.String())
	}
	if snap.Active {
		t.Fatalf("expected inactive, got %+v", snap)
	}
}

// TestLocalFile_MeteredServesRange guards the ServeFile→ServeContent swap: a
// Range request still yields 206 + Content-Range, the right bytes, and a
// video/* Content-Type inferred from the extension.
func TestLocalFile_MeteredServesRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := transferBrowser(t)
	content := bytes.Repeat([]byte("ABCDEFGH"), 1024) // 8 KiB
	if err := os.WriteFile(filepath.Join(dir, "clip.mp4"), content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	reg := localstream.NewRegistry(0)
	defer reg.Close()

	router := gin.New()
	router.GET("/api/local/file", LocalFile(b, reg, nil))

	req := httptest.NewRequest("GET", "/api/local/file?mount=Test&path=clip.mp4", nil)
	req.Header.Set("Range", "bytes=100-199")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("status=%d want 206; body len=%d", w.Code, w.Body.Len())
	}
	if cr := w.Header().Get("Content-Range"); cr != "bytes 100-199/8192" {
		t.Fatalf("Content-Range=%q want bytes 100-199/8192", cr)
	}
	if !bytes.Equal(w.Body.Bytes(), content[100:200]) {
		t.Fatalf("range body mismatch")
	}
	if ct := w.Header().Get("Content-Type"); ct == "" {
		t.Fatal("expected an inferred Content-Type for .mp4")
	}
}

// TestLocalFile_MeteredServesWhole checks the full-file path through the
// metered session returns the complete body.
func TestLocalFile_MeteredServesWhole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := transferBrowser(t)
	content := bytes.Repeat([]byte("z"), 5000)
	if err := os.WriteFile(filepath.Join(dir, "whole.mp4"), content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	reg := localstream.NewRegistry(0)
	defer reg.Close()

	router := gin.New()
	router.GET("/api/local/file", LocalFile(b, reg, nil))
	req := httptest.NewRequest("GET", "/api/local/file?mount=Test&path=whole.mp4", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), content) {
		t.Fatalf("body mismatch: got %d bytes want %d", w.Body.Len(), len(content))
	}
}
