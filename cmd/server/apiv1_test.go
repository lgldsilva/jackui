package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/config"
)

// The /api/v1 alias must reach the same routes as /api (issue #201): native
// clients pin /api/v1 while every path-sensitive middleware (BodyLimit
// exemptions, Guest/Device restrictions) keeps seeing the canonical /api path.

func TestAPIv1AliasReachesCanonicalRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotPath string
	inner := gin.New()
	inner.GET("/api/ping", func(c *gin.Context) {
		gotPath = c.Request.URL.Path
		c.String(http.StatusOK, "pong")
	})
	h := WithAPIv1Alias(inner)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil))
	if w.Code != http.StatusOK || w.Body.String() != "pong" {
		t.Fatalf("status=%d body=%q, want 200 pong", w.Code, w.Body.String())
	}
	if gotPath != "/api/ping" {
		t.Fatalf("handler saw path %q, want canonical /api/ping", gotPath)
	}
}

func TestAPIv1AliasPreservesQueryAndMethod(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotQuery string
	inner := gin.New()
	inner.POST("/api/echo", func(c *gin.Context) {
		gotQuery = c.Query("q")
		c.String(http.StatusOK, "ok")
	})
	h := WithAPIv1Alias(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/echo?q=mkv", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if gotQuery != "mkv" {
		t.Fatalf("query q=%q, want mkv", gotQuery)
	}
}

// apiReq runs req through h and returns the recorder.
func apiReq(h http.Handler, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

func TestAPIv1AliasLeavesOtherPathsAlone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "healthy") })
	r.GET("/api/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	h := WithAPIv1Alias(r)

	if w := apiReq(h, http.MethodGet, "/healthz"); w.Code != http.StatusOK || w.Body.String() != "healthy" {
		t.Fatalf("non-API path broken: %d %q", w.Code, w.Body.String())
	}
	// /api itself (no /v1) must be untouched.
	if w := apiReq(h, http.MethodGet, "/api/ping"); w.Code != http.StatusOK {
		t.Fatalf("canonical /api broken: %d", w.Code)
	}
	// /api/v1 without a following segment rewrites to /api — no route → 404,
	// never a panic or an accidental match.
	if w := apiReq(h, http.MethodGet, "/api/v1"); w.Code != http.StatusNotFound {
		t.Fatalf("/api/v1 = %d, want 404", w.Code)
	}
	// Look-alike prefixes must not be rewritten.
	if w := apiReq(h, http.MethodGet, "/api/v1foo"); w.Code != http.StatusNotFound {
		t.Fatalf("/api/v1foo = %d, want 404 (unrewritten)", w.Code)
	}
}

// End-to-end through setupRouter: the alias serves the same JSON as the
// canonical route on a real dependency graph.
func TestAPIv1AliasServesSetupRouterRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := setupRouter(&appDeps{cfg: &config.Config{Auth: config.AuthConfig{}}})
	h := WithAPIv1Alias(router)

	canon := apiReq(h, http.MethodGet, "/api/auth/config")
	alias := apiReq(h, http.MethodGet, "/api/v1/auth/config")
	if canon.Code != http.StatusOK || alias.Code != http.StatusOK {
		t.Fatalf("status canonical=%d alias=%d, want 200/200", canon.Code, alias.Code)
	}
	if canon.Body.String() != alias.Body.String() {
		t.Fatalf("body mismatch: canonical=%q alias=%q", canon.Body.String(), alias.Body.String())
	}
}
