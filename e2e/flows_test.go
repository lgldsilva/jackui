//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Flow specs: real user journeys end-to-end over HTTP, with cleanup.

// TestFlowRegisterAndAdminDelete registers a new account (SMTP disabled →
// links are logged), verifies it shows up in the admin user list as pending,
// and removes it again.
func TestFlowRegisterAndAdminDelete(t *testing.T) {
	username := fmt.Sprintf("e2e_flow_%d", time.Now().Unix())
	email := username + "@invalid"

	st, body := app.do(t, "POST", "/api/auth/register",
		fmt.Sprintf(`{"username":%q,"password":"E2eFlow!2026x","email":%q}`, username, email))
	if st != http.StatusOK && st != http.StatusCreated {
		t.Fatalf("register: want 200/201, got %d (body: %s)", st, snippet(body))
	}

	users, ub := app.do(t, "GET", "/api/auth/users", "")
	if users != http.StatusOK {
		t.Fatalf("list users: %d %s", users, snippet(ub))
	}
	var list []struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := jsonUnmarshal(ub, &list); err != nil {
		t.Fatalf("list users body: %v", err)
	}
	var id int64
	for _, u := range list {
		if u.Username == username {
			id = u.ID
		}
	}
	if id == 0 {
		t.Fatalf("registered user %q not found in admin list", username)
	}

	st, body = app.do(t, "DELETE", fmt.Sprintf("/api/auth/users/%d", id), "")
	if st != http.StatusOK {
		t.Fatalf("delete registered user: %d %s", st, snippet(body))
	}
}

// TestFlowPlaylistLifecycle creates a playlist, adds an item, reorders it,
// removes it and deletes the playlist.
func TestFlowPlaylistLifecycle(t *testing.T) {
	st, body := app.do(t, "POST", "/api/playlists",
		`{"name":"e2e-sweep-playlist","description":"e2e sweep lifecycle"}`)
	if st != http.StatusOK && st != http.StatusCreated {
		t.Fatalf("create playlist: %d %s", st, snippet(body))
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := jsonUnmarshal(body, &created); err != nil || created.ID == 0 {
		t.Fatalf("create playlist returned no id: %s", snippet(body))
	}
	pid := fmt.Sprintf("%d", created.ID)
	defer func() {
		st, _ := app.do(t, "DELETE", "/api/playlists/"+pid, "")
		if st != http.StatusOK {
			t.Errorf("cleanup: delete playlist %s: %d", pid, st)
		}
	}()

	st, body = app.do(t, "POST", "/api/playlists/"+pid+"/items",
		`{"title":"e2e item","magnet":"magnet:?xt=urn:btih:`+bogusHash+`","infoHash":"`+bogusHash+`","fileIndex":0}`)
	if st != http.StatusOK && st != http.StatusCreated {
		t.Fatalf("add item: %d %s", st, snippet(body))
	}
	var item struct {
		ID int64 `json:"id"`
	}
	if err := jsonUnmarshal(body, &item); err != nil || item.ID == 0 {
		t.Fatalf("add item returned no id: %s", snippet(body))
	}
	itemID := fmt.Sprintf("%d", item.ID)

	if st, body = app.do(t, "GET", "/api/playlists/"+pid, ""); st != http.StatusOK {
		t.Fatalf("get playlist: %d %s", st, snippet(body))
	}

	st, body = app.do(t, "PATCH", "/api/playlists/"+pid+"/items/"+itemID, `{"position":0}`)
	if st != http.StatusOK {
		t.Fatalf("reorder item: %d %s", st, snippet(body))
	}

	st, body = app.do(t, "DELETE", "/api/playlists/"+pid+"/items/"+itemID, "")
	if st != http.StatusOK {
		t.Fatalf("remove item: %d %s", st, snippet(body))
	}
}

// TestFlowLoginRefreshLogoutRoundTrip does a dedicated login, rotates the
// refresh token, and logs the session out; the consumed refresh token must
// stop working afterwards.
func TestFlowLoginRefreshLogoutRoundTrip(t *testing.T) {
	session := &apiClient{client: app.client}
	st, body, err := session.rawDo("POST", "/api/auth/login", "",
		fmt.Sprintf(`{"username":%q,"password":%q,"remember":false}`, env.adminUser, env.adminPass))
	if err != nil || st != http.StatusOK {
		t.Fatalf("login: st=%d err=%v", st, err)
	}
	var tok struct {
		Access  string `json:"access"`
		Refresh string `json:"refresh"`
	}
	if err := jsonUnmarshal(body, &tok); err != nil || tok.Access == "" || tok.Refresh == "" {
		t.Fatalf("login response shape: %s", snippet(body))
	}

	st, body, err = session.rawDo("POST", "/api/auth/refresh", "",
		fmt.Sprintf(`{"refresh":%q}`, tok.Refresh))
	if err != nil || st != http.StatusOK {
		t.Fatalf("refresh: st=%d err=%v body=%s", st, err, snippet(body))
	}
	var rotated struct {
		Access  string `json:"access"`
		Refresh string `json:"refresh"`
	}
	if err := jsonUnmarshal(body, &rotated); err != nil || rotated.Refresh == "" {
		t.Fatalf("refresh response shape: %s", snippet(body))
	}

	// The SPA sends the refresh token in the logout body (AuthContext.tsx:180);
	// that is what ConsumeRefreshToken revokes — mirror it exactly.
	st, body, err = session.rawDo("POST", "/api/auth/logout", rotated.Access,
		fmt.Sprintf(`{"refresh":%q}`, rotated.Refresh))
	if err != nil || st != http.StatusOK {
		t.Fatalf("logout: st=%d err=%v body=%s", st, err, snippet(body))
	}

	// The logged-out session's refresh token must be dead: reusing it after
	// logout must not mint new tokens.
	st, body, err = session.rawDo("POST", "/api/auth/refresh", "",
		fmt.Sprintf(`{"refresh":%q}`, rotated.Refresh))
	if err != nil {
		t.Fatalf("refresh after logout: %v", err)
	}
	if st == http.StatusOK {
		t.Fatalf("refresh token survived logout (status 200, body: %s)", snippet(body))
	}
}

// TestFlowFavoritesFolderLifecycle creates a favorites folder and deletes it.
func TestFlowFavoritesFolderLifecycle(t *testing.T) {
	name := fmt.Sprintf("e2e-folder-%d", time.Now().Unix())
	st, body := app.do(t, "POST", "/api/stream/favorites/folders",
		fmt.Sprintf(`{"name":%q}`, name))
	if st != http.StatusOK && st != http.StatusCreated {
		t.Fatalf("create folder: %d %s", st, snippet(body))
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := jsonUnmarshal(body, &created); err != nil || created.ID == 0 {
		t.Fatalf("create folder returned no id: %s", snippet(body))
	}
	st, body = app.do(t, "DELETE", fmt.Sprintf("/api/stream/favorites/folders/%d", created.ID), "")
	if st != http.StatusOK && st != http.StatusNoContent {
		t.Fatalf("delete folder: %d %s", st, snippet(body))
	}
}
