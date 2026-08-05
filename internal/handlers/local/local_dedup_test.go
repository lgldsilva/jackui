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
)

// writeFile is the package-level helper used by several local tests to lay
// out fixture files. Kept in this file because the dedup tests exercise it most.
func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func testDedupBrowser(t *testing.T) (*lb.Browser, string) {
	t.Helper()
	dir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Downloads", Path: dir}})
	return b, dir
}

func invokeDedup(t *testing.T, h gin.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})
	h(c)
	return w
}

func TestLocalDuplicates_MissingMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := testDedupBrowser(t)
	s := streamer.NewForTesting()
	w := invokeDedup(t, LocalDuplicates(b, s), http.MethodGet, "/api/local/duplicates?path=", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestLocalDuplicates_NoDuplicates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := testDedupBrowser(t)
	s := streamer.NewForTesting()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("unique"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("different"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := invokeDedup(t, LocalDuplicates(b, s), http.MethodGet, "/api/local/duplicates?mount=Downloads&path=", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"total":0`) {
		t.Errorf("expected total 0, got %s", w.Body.String())
	}
}

func TestLocalDuplicates_WithDuplicates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := testDedupBrowser(t)
	s := streamer.NewForTesting()
	content := []byte("same content")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	w := invokeDedup(t, LocalDuplicates(b, s), http.MethodGet, "/api/local/duplicates?mount=Downloads&path=", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"total":1`) {
		t.Errorf("expected total 1, got %s", w.Body.String())
	}
}

func TestLocalDuplicates_PathNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := testDedupBrowser(t)
	s := streamer.NewForTesting()
	w := invokeDedup(t, LocalDuplicates(b, s), http.MethodGet, "/api/local/duplicates?mount=Downloads&path=nonexistent", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestLocalDuplicatesDelete_MissingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := testDedupBrowser(t)
	s := streamer.NewForTesting()
	w := invokeDedup(t, LocalDuplicatesDelete(b, nil, s), http.MethodPost, "/api/local/duplicates/delete", `{"mount":"Downloads"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestFingerprintFile_Small(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(p, []byte("tiny"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, err := fingerprintFile(p, 4)
	if err != nil {
		t.Fatalf("fingerprintFile: %v", err)
	}
	h2, err := fingerprintFile(p, 4)
	if err != nil {
		t.Fatalf("fingerprintFile: %v", err)
	}
	if h1 != h2 {
		t.Error("same file must produce same fingerprint")
	}
}

func TestFingerprintFile_Different(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "a.txt")
	p2 := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(p1, []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p2, []byte("bbb"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, _ := fingerprintFile(p1, 3)
	h2, _ := fingerprintFile(p2, 3)
	if h1 == h2 {
		t.Error("different files must produce different fingerprints")
	}
}

func TestWithinBase(t *testing.T) {
	if !withinBase("/base", "/base/file") {
		t.Error("inside must be true")
	}
	if withinBase("/base", "/base") {
		t.Error("base itself must be false")
	}
	if withinBase("/base", "/etc") {
		t.Error("outside must be false")
	}
	if withinBase("/base", "/base/../etc") {
		t.Error("escape must be false")
	}
}

func TestGroupBySize(t *testing.T) {
	entries := []lb.Entry{
		{Path: "a", Size: 100},
		{Path: "b", Size: 100},
		{Path: "c", Size: 200},
	}
	groups := groupBySize(entries)
	if len(groups[100]) != 2 || len(groups[200]) != 1 {
		t.Errorf("groups = %v", groups)
	}
}
