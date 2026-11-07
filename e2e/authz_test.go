//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// apiGroupProbePaths lists one representative path per registered /api route
// (the full coverage lives in the per-family tables). The authz tests sweep
// them without a bearer and with a plain user, so an endpoint that lost its
// auth middleware fails loudly here.
var apiGroupProbePaths = []string{
	"/api/classify?q=e2e",
	"/api/status",
	"/api/search?q=e2e",
	"/api/search/stream?q=e2e",
	"/api/indexers",
	"/api/clients",
	"/api/history?page=1",
	"/api/history/results?query=e2e",
	"/api/history/cache?q=e2e",
	"/api/library",
	"/api/library/hash/" + bogusHash,
	"/api/library/" + bogusID,
	"/api/recommendations",
	"/api/stats",
	"/api/transfers",
	"/api/playlists",
	"/api/playlists/" + bogusID,
	"/api/watchlists",
	"/api/watchlists/" + bogusID + "/hits",
	"/api/notifications",
	"/api/push/vapid",
	"/api/downloads",
	"/api/downloads/filtered",
	"/api/downloads/trackers",
	"/api/downloads/categories",
	"/api/downloads/settings",
	"/api/downloads/destinations",
	"/api/downloads/promote/browse",
	"/api/promote/destinations",
	"/api/downloads/all",
	"/api/downloads/users",
	"/api/stream/cache",
	"/api/stream/rate",
	"/api/stream/active",
	"/api/stream/limits",
	"/api/stream/favorites",
	"/api/stream/favorites/folders",
	"/api/stream/info/" + bogusHash,
	"/api/stream/metadata/" + bogusHash,
	"/api/stream/health/" + bogusHash,
	"/api/stream/trackers/" + bogusHash,
	"/api/stream/art/" + bogusHash,
	"/api/stream/hls/" + bogusHash + "/" + bogusFile + "/index.m3u8",
	"/api/stream/sidecar/" + bogusHash + "/" + bogusFile,
	"/api/transcode/active",
	"/api/transcode/capabilities",
	"/api/settings/subtitles",
	"/api/subtitles/enabled",
	"/api/subtitles/search?q=e2e",
	"/api/subtitles/download/e2e-missing",
	"/api/subtitles/auto/" + bogusHash + "/" + bogusFile,
	"/api/tmdb/trending",
	"/api/tmdb/genres",
	"/api/tmdb/match?q=e2e",
	"/api/tmdb/videos?id=1",
	"/api/lyrics?q=e2e",
	"/api/music/trending",
	"/api/local/mounts",
	"/api/local/list",
	"/api/local/hidden",
	"/api/local/cache/status",
	"/api/local/duplicates",
	"/api/local/walk",
	"/api/local/probe",
	"/api/local/sidecars",
	"/api/local/audio/meta",
	"/api/auth/me",
	"/api/auth/mfa/backup-codes",
	"/api/auth/passkey",
	"/api/auth/users",
	"/api/config",
	"/api/stream/settings",
	"/api/mounts",
	"/api/ai/benchmark",
	"/api/metrics",
}

// TestAPIGroupRequiresAuth: every /api route must reject anonymous requests.
// 401 is the contract for auth.Required; nothing in the group may answer 200
// without a bearer.
func TestAPIGroupRequiresAuth(t *testing.T) {
	for _, p := range apiGroupProbePaths {
		t.Run("anon GET "+p, func(t *testing.T) {
			status, body, err := app.rawDo("GET", p, "", "")
			if err != nil {
				t.Fatalf("%v", err)
			}
			if status != http.StatusUnauthorized {
				t.Fatalf("want 401 for anonymous request, got %d (body: %s)", status, snippet(body))
			}
		})
	}
}

// TestAdminRoutesRejectPlainUser creates a role=user account, logs in as it,
// and asserts the admin-only routes answer 403.
func TestAdminRoutesRejectPlainUser(t *testing.T) {
	username := fmt.Sprintf("e2e_plain_%d", time.Now().Unix())
	password := "E2ePlain!2026x"

	adminStatus, body := app.do(t, "POST", "/api/auth/users",
		fmt.Sprintf(`{"username":%q,"password":%q,"role":"user"}`, username, password))
	if adminStatus != http.StatusOK {
		t.Fatalf("admin create plain user failed: %d %s", adminStatus, snippet(body))
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := jsonUnmarshal(body, &created); err != nil || created.ID == 0 {
		t.Fatalf("create user returned no id: %s", snippet(body))
	}
	defer func() {
		st, b := app.do(t, "DELETE", fmt.Sprintf("/api/auth/users/%d", created.ID), "")
		if st != http.StatusOK {
			t.Logf("cleanup: delete user %d failed: %d %s", created.ID, st, snippet(b))
		}
	}()

	user := &apiClient{client: app.client}
	var login struct {
		Access string `json:"access"`
	}
	st, body, err := user.rawDo("POST", "/api/auth/login", "",
		fmt.Sprintf(`{"username":%q,"password":%q}`, username, password))
	if err != nil || st != http.StatusOK {
		t.Fatalf("plain user login failed: st=%d err=%v body=%s", st, err, snippet(body))
	}
	if err := jsonUnmarshal(body, &login); err != nil || login.Access == "" {
		t.Fatalf("plain user login returned no token")
	}
	user.token = login.Access

	adminPaths := []string{
		"/api/config",
		"/api/stream/settings",
		"/api/mounts",
		"/api/ai/benchmark",
		"/api/auth/users",
		"/api/downloads/all",
		"/api/downloads/users",
		"/api/stream/cache",
		"/api/transcode/active",
	}
	for _, p := range adminPaths {
		t.Run("plain GET "+p, func(t *testing.T) {
			status, body, err := user.rawDo("GET", p, user.token, "")
			if err != nil {
				t.Fatalf("%v", err)
			}
			if status != http.StatusForbidden {
				t.Fatalf("want 403 for plain user on admin route, got %d (body: %s)", status, snippet(body))
			}
		})
	}
	t.Run("plain PUT /api/config", func(t *testing.T) {
		status, body, err := user.rawDo("PUT", "/api/config", user.token, `{"port":8989}`)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if status != http.StatusForbidden {
			t.Fatalf("want 403 for plain user PUT /api/config, got %d (body: %s)", status, snippet(body))
		}
	})
}

// TestUnknownAPIRouteIs404JSON pins the /api NoRoute contract: unknown API
// paths must answer JSON 404, never leak an HTML page.
func TestUnknownAPIRouteIs404JSON(t *testing.T) {
	status, body, err := app.rawDo("GET", "/api/definitely/not/a/route", app.token, "")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if status != http.StatusNotFound {
		t.Fatalf("want 404, got %d", status)
	}
	if !strings.Contains(body, "endpoint not found") {
		t.Fatalf("unknown /api route should return the JSON error, got %s", snippet(body))
	}
}
