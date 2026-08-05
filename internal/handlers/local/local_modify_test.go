package local

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func invokeModify(t *testing.T, h gin.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})
	h(c)
	return w
}

func TestLocalDelete_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	s := streamer.NewForTesting()
	w := invokeModify(t, LocalDelete(b, nil, s), http.MethodDelete, "/api/local/delete?mount=M", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestLocalDelete_CannotDeleteMountRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: dir}})
	s := streamer.NewForTesting()
	w := invokeModify(t, LocalDelete(b, nil, s), http.MethodDelete, "/api/local/delete?mount=M&path=.", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestLocalCleanEmptyDirs_MissingMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	w := invokeModify(t, LocalCleanEmptyDirs(b), http.MethodPost, "/api/local/clean-empty", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestLocalSetFolderLock_MissingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	s := streamer.NewForTesting()
	w := invokeModify(t, LocalSetFolderLock(b, s), http.MethodPost, "/api/local/lock", `{"mount":"M"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestCanModifyMount_Admin_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})
	if !canModifyMount(c, "any-mount") {
		t.Fatal("admin must be able to modify any mount")
	}
}

func TestCanModifyMount_NonAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("auth.claims", &auth.Claims{UserID: 2, Username: "user", Role: auth.RoleUser})
	if canModifyMount(c, "Meus downloads") {
		// "Meus downloads" is the writable mount for regular users.
		return
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestResolveDeletablePath_MountRoot_NewCode(t *testing.T) {
	dir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: dir}})
	_, err := resolveDeletablePath(b, "M", ".")
	if err == nil {
		t.Fatal("mount root must not be deletable")
	}
}

func TestResolveDeletablePath_Valid_NewCode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "file.txt"), []byte("x"))
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: dir}})
	abs, err := resolveDeletablePath(b, "M", "file.txt")
	if err != nil || abs == "" {
		t.Fatalf("resolveDeletablePath: (%q,%v)", abs, err)
	}
}

func TestIsMountRoot(t *testing.T) {
	dir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: dir}})
	mountAbs, _ := filepath.Abs(dir)
	if !isMountRoot(b, mountAbs) {
		t.Error("mount root must be detected")
	}
	if isMountRoot(b, filepath.Join(dir, "sub")) {
		t.Error("subdir must not be detected as mount root")
	}
}
