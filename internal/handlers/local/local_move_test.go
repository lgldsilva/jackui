package local

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

func invokeMove(t *testing.T, h gin.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})
	h(c)
	return w
}

func TestLocalMoveEntry_MissingFields_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	s := streamer.NewForTesting()
	tr := transfer.New()
	w := invokeMove(t, LocalMoveEntry(b, nil, s, tr), http.MethodPost, "/api/local/move", `{"srcMount":"M","srcPath":"a"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestLocalMoveEntry_NotAdmin_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	s := streamer.NewForTesting()
	tr := transfer.New()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/local/move", strings.NewReader(`{"srcMount":"M","srcPath":"a","dstMount":"M","dstPath":"b"}`))
	c.Set("auth.claims", &auth.Claims{UserID: 2, Username: "user", Role: auth.RoleUser})
	LocalMoveEntry(b, nil, s, tr)(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestValidateMoveRequest_MissingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"srcMount":"M"}`))
	_, ok := validateMoveRequest(c)
	if ok {
		t.Fatal("missing fields must not be ok")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestValidateMoveRequest_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"srcMount":"M","srcPath":"a","dstMount":"M","dstPath":"b"}`))
	_, ok := validateMoveRequest(c)
	if !ok {
		t.Fatal("valid request must be ok")
	}
}

func TestIsAdminMove_NotAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("auth.claims", &auth.Claims{UserID: 2, Username: "user", Role: auth.RoleUser})
	if isAdminMove(c) {
		t.Fatal("non-admin must not be allowed")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestIsSelfMove(t *testing.T) {
	st, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !isSelfMove(st, "/a/b", "/a/b/c") {
		t.Error("dir into itself must be detected")
	}
	if isSelfMove(st, "/a/b", "/a/c") {
		t.Error("sibling dir must not be detected as self")
	}
}

func TestCountTree_NewCode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), []byte("aa"))
	writeFile(t, filepath.Join(dir, "sub", "b.txt"), []byte("bbb"))
	files, bytes := CountTree(dir)
	if files != 2 || bytes != 5 {
		t.Errorf("CountTree = (%d,%d), want (2,5)", files, bytes)
	}
}

func TestBindRenameReq_MissingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"mount":"M"}`))
	_, ok := bindRenameReq(c)
	if ok {
		t.Fatal("missing fields must not be ok")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestBindRenameReq_InvalidName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"mount":"M","path":"a","newName":"../bad"}`))
	_, ok := bindRenameReq(c)
	if ok {
		t.Fatal("invalid name must not be ok")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestIsValidRenameName(t *testing.T) {
	valid := []string{"file.txt", "new name", "a"}
	for _, name := range valid {
		if !isValidRenameName(name) {
			t.Errorf("%q should be valid", name)
		}
	}
	invalid := []string{"", ".", "..", "a/b", "a\\b", "../bad"}
	for _, name := range invalid {
		if isValidRenameName(name) {
			t.Errorf("%q should be invalid", name)
		}
	}
}

func TestResolveRenameDest_SameName(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	writeFile(t, src, []byte("x"))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	_, ok := resolveRenameDest(c, src, "a.txt")
	if ok {
		t.Fatal("same name must not be ok")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestResolveRenameDest_Clobber(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	writeFile(t, src, []byte("x"))
	writeFile(t, filepath.Join(dir, "b.txt"), []byte("y"))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	_, ok := resolveRenameDest(c, src, "b.txt")
	if ok {
		t.Fatal("clobber must not be ok")
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

func TestResolveRenameDest_Valid(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	writeFile(t, src, []byte("x"))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	dst, ok := resolveRenameDest(c, src, "b.txt")
	if !ok || dst != filepath.Join(dir, "b.txt") {
		t.Fatalf("resolveRenameDest = (%q,%v)", dst, ok)
	}
}

func TestMovePathJob_Rename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	writeFile(t, src, []byte("content"))
	st, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	tr := transfer.New()
	job := tr.StartFor(1, "test", "move", 1, 7)
	dst := filepath.Join(dir, "b.txt")
	if err := MovePathJob(src, dst, st, job, 1, 7); err != nil {
		t.Fatalf("MovePathJob: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatal("destination must exist")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source must be removed")
	}
}
