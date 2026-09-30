package local

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// sanitizeRenameName is the rename Join's path-injection barrier: only the
// cleaned return value reaches filepath.Join.
func TestSanitizeRenameName(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, "../bad"} {
		if clean, ok := sanitizeRenameName(bad); ok || clean != "" {
			t.Errorf("sanitizeRenameName(%q) = (%q, %v), want (\"\", false)", bad, clean, ok)
		}
	}
	if clean, ok := sanitizeRenameName("file.txt"); !ok || clean != "file.txt" {
		t.Errorf("sanitizeRenameName(file.txt) = (%q, %v), want passthrough", clean, ok)
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

func TestResolveRenameDest_InvalidName(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	writeFile(t, src, []byte("x"))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	_, ok := resolveRenameDest(c, src, "../b.txt")
	if ok {
		t.Fatal("name with a path separator must not be ok")
	}
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid name") {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
}

// ─── Atomic cross-filesystem copy (hardening fix) ───────────────────────────
//
// copyFileAndRemoveJob used to write the destination directly with O_TRUNC and
// never fsynced before removing the source — a kill -9 / power loss mid-copy
// could leave a truncated destination while the complete source was gone.

// partialPath is the scratch name the atomic copy writes before the final
// rename (kept in sync with copyFileAndRemoveJob).
func partialPath(dst string) string { return dst + ".partial" }

// A stale scratch file from a copy killed mid-flight (the atomic scheme's
// crash signature: scratch present, no dst) must not wedge the next attempt —
// the move completes, consumes the scratch file and only then removes the src.
func TestCopyFileAndRemoveJob_StalePartialFromCrashIsRecovered(t *testing.T) {
	dir := t.TempDir()
	payload := bytes.Repeat([]byte("A"), 100)
	src := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.mkv")
	// What a kill -9 mid-copy leaves: scratch file, no final destination.
	if err := os.WriteFile(partialPath(dst), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFileAndRemove(src, dst, st); err != nil {
		t.Fatalf("copyFileAndRemove: %v", err)
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("dst content mismatch: %d bytes, want %d", len(body), len(payload))
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src should be removed after the copy completed, stat err=%v", err)
	}
	if _, err := os.Stat(partialPath(dst)); !os.IsNotExist(err) {
		t.Errorf("scratch copy %s must be consumed by the final rename", partialPath(dst))
	}
}

// A truncated destination (crash signature of the old non-atomic copy) must be
// fully recopied, never kept as "already done".
func TestCopyFileAndRemoveJob_TruncatedDstIsRecopied(t *testing.T) {
	dir := t.TempDir()
	payload := bytes.Repeat([]byte("B"), 80)
	src := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.mkv")
	if err := os.WriteFile(dst, bytes.Repeat([]byte("C"), 10), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFileAndRemove(src, dst, st); err != nil {
		t.Fatalf("copyFileAndRemove: %v", err)
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("destination holds a TRUNCATED copy (%d bytes, want %d)", len(body), len(payload))
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src should be removed after the recopy, stat err=%v", err)
	}
}

// Resume contract: a destination with the SAME size as the source is treated as
// already copied — the source is removed WITHOUT recopying (the decoy content
// proves the copy was skipped).
func TestCopyFileAndRemoveJob_SameSizeDstResumesWithoutRecopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(src, bytes.Repeat([]byte("D"), 50), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.mkv")
	decoy := bytes.Repeat([]byte("E"), 50) // same size, different content
	if err := os.WriteFile(dst, decoy, 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFileAndRemove(src, dst, st); err != nil {
		t.Fatalf("copyFileAndRemove: %v", err)
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, decoy) {
		t.Fatal("same-size destination was recopied — the resume/skip contract broke")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src should be removed on the resume path, stat err=%v", err)
	}
}

// The atomic rename must preserve the source's mode and mtime (the old
// copy+Chtimes contract — date sort and mtime-based scans depend on it).
func TestCopyFileAndRemoveJob_PreservesModeAndMtime(t *testing.T) {
	dir := t.TempDir()
	payload := bytes.Repeat([]byte("F"), 30)
	src := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := os.Chtimes(src, old, old); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.mkv")

	st, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFileAndRemove(src, dst, st); err != nil {
		t.Fatalf("copyFileAndRemove: %v", err)
	}
	got, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode().Perm() != 0o600 {
		t.Fatalf("dst mode = %v, want 0600", got.Mode().Perm())
	}
	if !got.ModTime().Equal(old) {
		t.Fatalf("dst mtime = %v, want %v", got.ModTime(), old)
	}
}
