package local

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	lb "github.com/lgldsilva/jackui/internal/local"
)

func TestOriginalLocalPaths(t *testing.T) {
	req := &localPromoteReq{Paths: []string{"a", "b"}}
	if got := originalLocalPaths(req); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("paths = %v", got)
	}

	req2 := &localPromoteReq{Path: "single"}
	if got := originalLocalPaths(req2); len(got) != 1 || got[0] != "single" {
		t.Errorf("single = %v", got)
	}

	req3 := &localPromoteReq{}
	if got := originalLocalPaths(req3); got != nil {
		t.Errorf("empty = %v, want nil", got)
	}
}

func TestLocalPromoteTargetDir(t *testing.T) {
	if got, err := localPromoteTargetDir("/base", ""); err != nil || got != "/base" {
		t.Errorf("empty subdir = (%q,%v)", got, err)
	}
	if got, err := localPromoteTargetDir("/base", "movies"); err != nil || got != "/base/movies" {
		t.Errorf("subdir = (%q,%v)", got, err)
	}
	if _, err := localPromoteTargetDir("/base", "../escape"); err == nil {
		t.Error("traversal must error")
	}
}

func TestPromoteJobLabel(t *testing.T) {
	if got := promoteJobLabel(&localPromoteReq{}, []string{"a"}); got != "a" {
		t.Errorf("single path = %q", got)
	}
	if got := promoteJobLabel(&localPromoteReq{Path: "single"}, nil); got != "single" {
		t.Errorf("single Path = %q", got)
	}
	if got := promoteJobLabel(&localPromoteReq{}, []string{"a", "b", "c"}); got != "3 itens" {
		t.Errorf("multiple = %q", got)
	}
}

func TestCurrentDirOf_Promote(t *testing.T) {
	if got := currentDirOf("file.txt"); got != "" {
		t.Errorf("root = %q, want empty", got)
	}
	if got := currentDirOf("dir/file.txt"); got != "dir" {
		t.Errorf("dir = %q", got)
	}
	if got := currentDirOf("dir/sub/file.txt"); got != "dir/sub" {
		t.Errorf("nested = %q", got)
	}
}

func TestResolveLocalPaths(t *testing.T) {
	tempDir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Downloads", Path: tempDir}})

	req := &localPromoteReq{Mount: "Downloads", Paths: []string{"a", "b"}}
	got := resolveLocalPaths(b, req, "")
	if len(got) != 2 {
		t.Errorf("paths = %v", got)
	}

	req2 := &localPromoteReq{Mount: "Downloads", Path: "single"}
	got2 := resolveLocalPaths(b, req2, "")
	if len(got2) != 1 {
		t.Errorf("single = %v", got2)
	}

	req3 := &localPromoteReq{Mount: "Downloads"}
	got3 := resolveLocalPaths(b, req3, "")
	if got3 != nil {
		t.Errorf("empty = %v, want nil", got3)
	}
}

func TestExtractLocalPromoteReq_MissingMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Downloads", Path: tempDir}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/promote", strings.NewReader(`{"path":"file"}`))
	c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})

	_, _, ok := extractLocalPromoteReq(c, b, "/shared", nil)
	if ok {
		t.Fatal("missing mount must not be ok")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestExtractLocalPromoteReq_InvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Downloads", Path: tempDir}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/promote", strings.NewReader(`{`))
	c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})

	_, _, ok := extractLocalPromoteReq(c, b, "/shared", nil)
	if ok {
		t.Fatal("invalid JSON must not be ok")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
