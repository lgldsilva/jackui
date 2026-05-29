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

// TestLocalUserScopeIsolation verifies that on a UserSubpath mount a logged-in
// user can only reach files under their own subdir — even when they craft a
// path pointing at another user's subdir, and even when authenticated with a
// media token (?token=). Regression guard for the IDOR where play/HLS/probe/
// subtitle handlers resolved the raw path against the mount root, and for the
// media-token scope-collapse (userFromCtx must keep the username).
func TestLocalUserScopeIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mountRoot := t.TempDir()
	// bob's private file lives at <root>/bob/private.mkv — alice must NOT read it.
	if err := os.MkdirAll(filepath.Join(mountRoot, "bob"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mountRoot, "bob", "private.mkv"), []byte("BOB-SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	// alice's own file at <root>/alice/mine.mkv — alice reaches it via path=mine.mkv.
	if err := os.MkdirAll(filepath.Join(mountRoot, "alice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mountRoot, "alice", "mine.mkv"), []byte("ALICE-OWN"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Meus downloads", Path: mountRoot, UserSubpath: true},
	})

	newRouter := func(claims *auth.Claims) *gin.Engine {
		r := gin.New()
		r.Use(withClaims(claims))
		r.GET("/api/local/file", LocalFile(b))
		r.GET("/api/local/play", LocalPlay(b))
		return r
	}

	get := func(r *gin.Engine, url string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		return w
	}

	accessAlice := &auth.Claims{UserID: 1, Username: "alice", Role: auth.RoleUser}
	mediaAlice := &auth.Claims{UserID: 1, Username: "alice", Role: auth.RoleUser, Scope: auth.ScopeMedia}

	t.Run("access token cannot read another user's file via crafted path", func(t *testing.T) {
		w := get(newRouter(accessAlice), "/api/local/file?mount=Meus+downloads&path=bob/private.mkv")
		if w.Code == http.StatusOK {
			t.Fatalf("IDOR: alice read bob's file (status 200, body=%q)", w.Body.String())
		}
		if w.Body.String() == "BOB-SECRET" {
			t.Fatalf("IDOR: bob's content leaked to alice")
		}
	})

	t.Run("media token cannot read another user's file (scope must not collapse)", func(t *testing.T) {
		w := get(newRouter(mediaAlice), "/api/local/file?mount=Meus+downloads&path=bob/private.mkv&token=x")
		if w.Code == http.StatusOK || w.Body.String() == "BOB-SECRET" {
			t.Fatalf("IDOR via media token: status=%d body=%q", w.Code, w.Body.String())
		}
	})

	t.Run("LocalPlay denies another user's file", func(t *testing.T) {
		w := get(newRouter(accessAlice), "/api/local/play?mount=Meus+downloads&path=bob/private.mkv")
		if w.Code == http.StatusOK {
			t.Fatalf("IDOR: LocalPlay resolved bob's file for alice (body=%q)", w.Body.String())
		}
	})

	t.Run("user reads own file via logical path", func(t *testing.T) {
		w := get(newRouter(accessAlice), "/api/local/file?mount=Meus+downloads&path=mine.mkv")
		if w.Code != http.StatusOK {
			t.Fatalf("alice could not read her own file: status=%d", w.Code)
		}
		if w.Body.String() != "ALICE-OWN" {
			t.Fatalf("unexpected body for alice's own file: %q", w.Body.String())
		}
	})

	t.Run("media token reads own file via logical path", func(t *testing.T) {
		w := get(newRouter(mediaAlice), "/api/local/file?mount=Meus+downloads&path=mine.mkv&token=x")
		if w.Code != http.StatusOK || w.Body.String() != "ALICE-OWN" {
			t.Fatalf("media token could not read own file: status=%d body=%q", w.Code, w.Body.String())
		}
	})

	admin := &auth.Claims{UserID: 9, Username: "admin", Role: auth.RoleAdmin}

	t.Run("admin can view another user's file via ?user= override", func(t *testing.T) {
		w := get(newRouter(admin), "/api/local/file?mount=Meus+downloads&path=private.mkv&user=bob")
		if w.Code != http.StatusOK || w.Body.String() != "BOB-SECRET" {
			t.Fatalf("admin could not view bob's file via ?user=bob: status=%d body=%q", w.Code, w.Body.String())
		}
	})

	t.Run("admin without ?user= stays scoped to own subdir", func(t *testing.T) {
		// admin has no admin/ subdir → her own scope can't reach bob's file.
		w := get(newRouter(admin), "/api/local/file?mount=Meus+downloads&path=private.mkv")
		if w.Code == http.StatusOK && w.Body.String() == "BOB-SECRET" {
			t.Fatalf("admin leaked bob's file without selecting a user")
		}
	})

	t.Run("non-admin ?user= override is ignored (cannot cross boundary)", func(t *testing.T) {
		w := get(newRouter(accessAlice), "/api/local/file?mount=Meus+downloads&path=private.mkv&user=bob")
		if w.Code == http.StatusOK && w.Body.String() == "BOB-SECRET" {
			t.Fatalf("privilege escalation: alice used ?user=bob to read bob's file")
		}
	})
}
