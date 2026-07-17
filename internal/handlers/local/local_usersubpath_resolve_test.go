package local

import (
	"encoding/json"
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

// Regression: on a UserSubpath mount, LocalDuplicates/LocalCacheFolder used to
// StripUserScope the walk entries BEFORE resolving them, so the downstream
// ResolvePath (empty user) looked for `<mount>/<rel>` instead of the real
// `<mount>/<user>/<rel>`. Duplicates always returned 0 and folder-cache primed
// the wrong path/key. The scope prefix must only be stripped for the hidden
// compare, never off the entry that gets resolved.

func userSubpathMount(t *testing.T) (*lb.Browser, string) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// alice's per-user subtree: two identical files (a duplicate pair).
	write("alice/f/one.mkv", "identical dedup payload — long enough to hash")
	write("alice/f/two.mkv", "identical dedup payload — long enough to hash")
	b := lb.NewBrowser([]config.ExternalMount{{Name: "T", Path: root, UserSubpath: true}})
	return b, root
}

func TestLocalDuplicates_UserSubpathResolves(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := userSubpathMount(t)
	alice := &auth.Claims{UserID: 1, Username: "alice", Role: auth.RoleUser}

	r := gin.New()
	r.Use(withClaims(alice))
	r.GET("/api/local/duplicates", LocalDuplicates(b, nil)) // s=nil → no curtain

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/local/duplicates?mount=T", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", w.Code, w.Body.String())
	}
	var resp struct {
		Total  int `json:"total"`
		Groups []struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Before the fix this was 0: fingerprintGroup resolved the scope-stripped
	// "f/one.mkv" → <root>/f/one.mkv (missing) and never hashed anything.
	if resp.Total != 1 || len(resp.Groups) != 1 || len(resp.Groups[0].Files) != 2 {
		t.Fatalf("expected 1 group of 2 dup files on a UserSubpath mount, got total=%d groups=%+v", resp.Total, resp.Groups)
	}
}

// TestUserSubpathResolveContract pins the exact invariant both LocalDuplicates
// and LocalCacheFolder depend on: entries flowing into ResolvePath must carry the
// FULL scoped path. It documents why StripUserScope-before-resolve was the bug —
// the stripped spelling resolves to a non-existent sibling of the per-user root.
func TestUserSubpathResolveContract(t *testing.T) {
	b, root := userSubpathMount(t)

	full, err := b.ResolvePath("T", "alice/f/one.mkv")
	if err != nil {
		t.Fatalf("resolve full: %v", err)
	}
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("full scoped path must resolve to the real file, got %q: %v", full, err)
	}
	if full != filepath.Join(root, "alice", "f", "one.mkv") {
		t.Fatalf("resolved abs = %q, want under alice/", full)
	}

	// The scope-stripped spelling (the old bug) resolves to the wrong location.
	stripped, err := b.ResolvePath("T", "f/one.mkv")
	if err != nil {
		t.Fatalf("resolve stripped: %v", err)
	}
	if _, err := os.Stat(stripped); err == nil {
		t.Fatalf("scope-stripped path %q must NOT resolve to a real file", stripped)
	}
}
