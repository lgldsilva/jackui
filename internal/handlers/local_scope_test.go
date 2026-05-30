package handlers

import (
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

// withClaims injects auth Claims into the gin context, mimicking what the
// Required/Optional middleware does after parsing a token. Used to drive the
// per-user scoping logic in handlers without standing up the full JWT stack.
func withClaims(claims *auth.Claims) gin.HandlerFunc {
	return func(c *gin.Context) {
		if claims != nil {
			c.Set("auth.claims", claims)
		}
		c.Next()
	}
}

func setUpLocalScopeMount(t *testing.T) (*local.Browser, string) {
	t.Helper()
	mountRoot := t.TempDir()
	writeFile := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(mountRoot, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mountRoot, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("bob/private.mkv", "BOB-SECRET")
	writeFile("alice/mine.mkv", "ALICE-OWN")
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Meus downloads", Path: mountRoot, UserSubpath: true},
	})
	return b, "BOB-SECRET"
}

func newLocalRouter(b *local.Browser, claims *auth.Claims) *gin.Engine {
	r := gin.New()
	r.Use(withClaims(claims))
	r.GET("/api/local/file", LocalFile(b))
	r.GET("/api/local/play", LocalPlay(b))
	return r
}

func scopeGet(b *local.Browser, claims *auth.Claims, url string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := newLocalRouter(b, claims)
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

func assertDenied(t *testing.T, w *httptest.ResponseRecorder, secret string) {
	t.Helper()
	if w.Code == http.StatusOK || w.Body.String() == secret {
		t.Fatalf("access should be denied: status=%d body=%q", w.Code, w.Body.String())
	}
}

func assertServed(t *testing.T, w *httptest.ResponseRecorder, content string) {
	t.Helper()
	if w.Code != http.StatusOK || w.Body.String() != content {
		t.Fatalf("expected status 200 body %q, got status=%d body=%q", content, w.Code, w.Body.String())
	}
}

func assertNotLeaked(t *testing.T, w *httptest.ResponseRecorder, secret string) {
	t.Helper()
	if w.Code == http.StatusOK && w.Body.String() == secret {
		t.Fatalf("secret leaked: status=%d body=%q", w.Code, w.Body.String())
	}
}

// TestLocalUserScopeIsolation verifies that on a UserSubpath mount a logged-in
// user can only reach files under their own subdir — even when they craft a
// path pointing at another user's subdir, and even when authenticated with a
// media token (?token=). Regression guard for the IDOR where play/HLS/probe/
// subtitle handlers resolved the raw path against the mount root, and for the
// media-token scope-collapse (userFromCtx must keep the username).
func TestLocalUserScopeIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, secret := setUpLocalScopeMount(t)

	accessAlice := &auth.Claims{UserID: 1, Username: "alice", Role: auth.RoleUser}
	mediaAlice := &auth.Claims{UserID: 1, Username: "alice", Role: auth.RoleUser, Scope: auth.ScopeMedia}
	admin := &auth.Claims{UserID: 9, Username: "admin", Role: auth.RoleAdmin}

	t.Run("access token cannot read another user's file via crafted path", func(t *testing.T) {
		assertDenied(t, scopeGet(b, accessAlice, "/api/local/file?mount=Meus+downloads&path=bob/private.mkv"), secret)
	})

	t.Run("media token cannot read another user's file (scope must not collapse)", func(t *testing.T) {
		w := scopeGet(b, mediaAlice, "/api/local/file?mount=Meus+downloads&path=bob/private.mkv&token=x")
		assertDenied(t, w, secret)
	})

	t.Run("LocalPlay denies another user's file", func(t *testing.T) {
		w := scopeGet(b, accessAlice, "/api/local/play?mount=Meus+downloads&path=bob/private.mkv")
		if w.Code == http.StatusOK {
			t.Fatalf("IDOR: LocalPlay resolved bob's file for alice (body=%q)", w.Body.String())
		}
	})

	t.Run("user reads own file via logical path", func(t *testing.T) {
		assertServed(t, scopeGet(b, accessAlice, "/api/local/file?mount=Meus+downloads&path=mine.mkv"), "ALICE-OWN")
	})

	t.Run("media token reads own file via logical path", func(t *testing.T) {
		assertServed(t, scopeGet(b, mediaAlice, "/api/local/file?mount=Meus+downloads&path=mine.mkv&token=x"), "ALICE-OWN")
	})

	t.Run("admin can view another user's file via ?user= override", func(t *testing.T) {
		assertServed(t, scopeGet(b, admin, "/api/local/file?mount=Meus+downloads&path=private.mkv&user=bob"), secret)
	})

	t.Run("admin without ?user= stays scoped to own subdir", func(t *testing.T) {
		w := scopeGet(b, admin, "/api/local/file?mount=Meus+downloads&path=private.mkv")
		assertNotLeaked(t, w, secret)
	})

	t.Run("non-admin ?user= override is ignored (cannot cross boundary)", func(t *testing.T) {
		w := scopeGet(b, accessAlice, "/api/local/file?mount=Meus+downloads&path=private.mkv&user=bob")
		assertNotLeaked(t, w, secret)
	})
}
