package local

import (
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

// TestLocalFile_LocalDiskServesRangeDirectly proves a local-disk file is served
// with correct Range/206 semantics even when a read-ahead Registry is attached.
// Local disk takes the fast http.ServeFile path (instant seeks) instead of the
// 16 MB synchronous read-ahead Session reserved for remote/FUSE mounts — the
// routing decision must consult isRemoteFS.
func TestLocalFile_LocalDiskServesRangeDirectly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	content := []byte("0123456789abcdefghij") // 20 bytes
	if err := os.WriteFile(filepath.Join(mountDir, "clip.mp4"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Test", Path: mountDir}})
	reg := localstream.NewRegistry(16) // non-nil: pre-fix this forced the slow Session

	called := false
	prev := isRemoteFS
	isRemoteFS = func(string) bool { called = true; return false } // local disk
	t.Cleanup(func() { isRemoteFS = prev })

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/file?mount=Test&path=clip.mp4", nil)
	c.Request.Header.Set("Range", "bytes=5-9")
	LocalFile(b, reg, nil)(c)

	if !called {
		t.Fatal("isRemoteFS not consulted — local files must skip the read-ahead Session")
	}
	if w.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206; body %q", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "56789" {
		t.Errorf("range body = %q, want %q", got, "56789")
	}
	if cr := w.Header().Get("Content-Range"); cr != "bytes 5-9/20" {
		t.Errorf("Content-Range = %q, want %q", cr, "bytes 5-9/20")
	}
}

// TestLocalFile_RemoteMountUsesReadAheadSession proves the remote/FUSE branch
// (metered read-ahead Session) still honours Range correctly.
func TestLocalFile_RemoteMountUsesReadAheadSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	content := []byte("0123456789abcdefghij")
	if err := os.WriteFile(filepath.Join(mountDir, "clip.mp4"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Test", Path: mountDir}})
	reg := localstream.NewRegistry(16)

	prev := isRemoteFS
	isRemoteFS = func(string) bool { return true } // pretend rclone/FUSE
	t.Cleanup(func() { isRemoteFS = prev })

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/file?mount=Test&path=clip.mp4", nil)
	c.Request.Header.Set("Range", "bytes=5-9")
	LocalFile(b, reg, nil)(c)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206; body %q", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "56789" {
		t.Errorf("range body = %q, want %q", got, "56789")
	}
}
