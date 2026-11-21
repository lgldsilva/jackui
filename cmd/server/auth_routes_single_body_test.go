package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
)

// TestAuthRoutesSingleJSONBodyOn401 is the regression test for jackui#286:
// auth middleware rejections on registerAuthRoutes routes must produce ONE
// JSON body. When auth.Required responds without aborting, the group handler
// (e.g. handlers.Me) appends a second {"error","code"} object and the body
// becomes invalid JSON ({...}{...}), breaking strict parsers and the
// OpenAPI response validation.
func TestAuthRoutesSingleJSONBodyOn401(t *testing.T) {
	gin.SetMode(gin.TestMode)

	deps := &appDeps{
		cfg:       &config.Config{},
		authStore: &auth.Store{},
		tokenMgr:  auth.NewTokenManager([]byte("test-secret-0123456789abcdef0123"), 15*time.Minute),
	}
	router := setupRouter(deps)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/auth/me"},
		{http.MethodPost, "/api/auth/password"},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: expected 401, got %d", tc.method, tc.path, rec.Code)
		}
		if !json.Valid(rec.Body.Bytes()) {
			t.Fatalf("%s %s: response body is not a single JSON object: %s", tc.method, tc.path, rec.Body.String())
		}
	}
}
