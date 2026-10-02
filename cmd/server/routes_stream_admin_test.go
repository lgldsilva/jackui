package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

const testHash40 = "0123456789012345678901234567890123456789"

// streamRoleRouter registers the production stream routes behind a claims
// middleware carrying the given role, with AdminOnly on the admin subgroup —
// the same composition setupRouter builds when auth is enabled.
func streamRoleRouter(t *testing.T, role auth.Role) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	deps := &appDeps{cfg: &config.Config{}, streamSrv: streamer.NewForTesting(), hlsMgr: mgr}
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "t", Role: role})
		c.Next()
	})
	adminAPI := api.Group("")
	adminAPI.Use(auth.AdminOnly())
	registerStreamRoutes(api, adminAPI, deps)
	return r
}

func doReq(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body == "" {
		rd = bytes.NewReader(nil)
	} else {
		rd = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Global destructive stream mutations (drop one torrent, batch drop) hit the
// shared swarm for EVERY user, so they sit behind AdminOnly — matching the
// module invariant ("global mutations that affect every user are admin-only").
func TestStreamDestructiveMutationsAreAdminOnly(t *testing.T) {
	paths := []struct {
		method, path, body string
	}{
		{http.MethodDelete, "/api/stream/" + testHash40, ""},
		{http.MethodPost, "/api/stream/drop/batch", `{"hashes":["` + testHash40 + `"]}`},
	}

	for _, p := range paths {
		user := doReq(streamRoleRouter(t, auth.RoleUser), p.method, p.path, p.body)
		if user.Code != http.StatusForbidden {
			t.Errorf("%s %s as non-admin: status = %d, want 403", p.method, p.path, user.Code)
		}
		admin := doReq(streamRoleRouter(t, auth.RoleAdmin), p.method, p.path, p.body)
		if admin.Code == http.StatusForbidden {
			t.Errorf("%s %s as admin: got 403 (route wrongly fenced)", p.method, p.path)
		}
	}
}

// GET /stream/cache lists EVERY cached torrent's name + info hash + isFavorite
// flag across ALL users — a cross-user surface the UI only renders for admins,
// so the route must sit behind AdminOnly: a regular user gets 403, an admin
// gets through.
func TestStreamCacheReadIsAdminOnly(t *testing.T) {
	user := doReq(streamRoleRouter(t, auth.RoleUser), http.MethodGet, "/api/stream/cache", "")
	if user.Code != http.StatusForbidden {
		t.Errorf("GET /api/stream/cache as non-admin: status = %d, want 403", user.Code)
	}
	admin := doReq(streamRoleRouter(t, auth.RoleAdmin), http.MethodGet, "/api/stream/cache", "")
	if admin.Code == http.StatusForbidden {
		t.Errorf("GET /api/stream/cache as admin: got 403 (route wrongly fenced)")
	}
}

// The playback-affecting-but-non-destructive siblings must stay reachable for
// regular users (moving THESE would break the player).
func TestStreamPlaybackMutationsStayUserReachable(t *testing.T) {
	keep := []struct {
		method, path, body string
	}{
		{http.MethodPost, "/api/stream/" + testHash40 + "/pause", ""},
		{http.MethodPost, "/api/stream/" + testHash40 + "/resume", ""},
		{http.MethodPost, "/api/stream/" + testHash40 + "/priority", `{"priority":1}`},
		{http.MethodPost, "/api/stream/" + testHash40 + "/viewer", ""},
		{http.MethodDelete, "/api/stream/" + testHash40 + "/viewer", ""},
	}
	for _, p := range keep {
		user := doReq(streamRoleRouter(t, auth.RoleUser), p.method, p.path, p.body)
		if user.Code == http.StatusForbidden {
			t.Errorf("%s %s as non-admin: got 403 — playback route must stay user-accessible", p.method, p.path)
		}
	}
}
