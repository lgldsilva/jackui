package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbtest"
)

// adminUsersRouter wires the admin user-management routes exactly like
// registerAuthRoutes does (claims middleware + AdminOnly), so the 403 fence is
// part of what's under test.
func adminUsersRouter(store *auth.Store, claims *auth.Claims) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if claims != nil {
			c.Set("auth.claims", claims)
		}
		c.Next()
	})
	grp := r.Group("/api/auth/users")
	grp.Use(auth.AdminOnly())
	grp.POST("/:id/reset-password", AdminResetPassword(store, nil, ""))
	grp.GET("/:id/sessions", AdminListUserSessions(store))
	grp.DELETE("/:id/sessions", AdminRevokeUserSessions(store))
	grp.DELETE("/:id/sessions/:sid", AdminRevokeUserSession(store))
	return r
}

func adminClaims() *auth.Claims {
	return &auth.Claims{UserID: 99, Username: "root", Role: auth.RoleAdmin}
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body == "" {
		rd = bytes.NewReader(nil)
	} else {
		rd = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAdminUserRoutes_NonAdminForbidden(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "alice", "secret123")
	r := adminUsersRouter(store, &auth.Claims{UserID: user.ID, Username: "alice", Role: auth.RoleUser})

	paths := []struct{ method, path string }{
		{"POST", "/api/auth/users/1/reset-password"},
		{"GET", "/api/auth/users/1/sessions"},
		{"DELETE", "/api/auth/users/1/sessions"},
		{"DELETE", "/api/auth/users/1/sessions/abc"},
	}
	for _, p := range paths {
		w := doJSON(t, r, p.method, p.path, "")
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403", p.method, p.path, w.Code)
		}
	}
}

func TestAdminUserRoutes_UserNotFound(t *testing.T) {
	store := newAuthStore(t)
	r := adminUsersRouter(store, adminClaims())

	paths := []struct{ method, path string }{
		{"POST", "/api/auth/users/12345/reset-password"},
		{"GET", "/api/auth/users/12345/sessions"},
		{"DELETE", "/api/auth/users/12345/sessions"},
		{"DELETE", "/api/auth/users/12345/sessions/abc"},
	}
	for _, p := range paths {
		w := doJSON(t, r, p.method, p.path, "")
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404; body %s", p.method, p.path, w.Code, w.Body.String())
		}
	}
}

func TestAdminUserRoutes_InvalidID(t *testing.T) {
	store := newAuthStore(t)
	r := adminUsersRouter(store, adminClaims())
	w := doJSON(t, r, "GET", "/api/auth/users/not-a-number/sessions", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestAdminResetPassword_WithPassword(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "alice", "oldpass1")
	if _, err := store.CreateRefreshToken(user.ID, time.Hour, false, "UA", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	r := adminUsersRouter(store, adminClaims())

	w := doJSON(t, r, "POST", "/api/auth/users/"+itoa(user.ID)+"/reset-password", `{"password":"newpass1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	if _, err := store.VerifyPassword("alice", "newpass1"); err != nil {
		t.Errorf("new password should work: %v", err)
	}
	if _, err := store.VerifyPassword("alice", "oldpass1"); err == nil {
		t.Error("old password should be dead")
	}
	sessions, _ := store.ListSessions(user.ID, "")
	if len(sessions) != 0 {
		t.Errorf("sessions after reset = %d, want 0 (all revoked)", len(sessions))
	}
}

func TestAdminResetPassword_ShortPassword(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "alice", "oldpass1")
	r := adminUsersRouter(store, adminClaims())

	w := doJSON(t, r, "POST", "/api/auth/users/"+itoa(user.ID)+"/reset-password", `{"password":"abc"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAdminResetPassword_LinkMode(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "alice", "oldpass1")
	if _, err := store.CreateRefreshToken(user.ID, time.Hour, false, "", ""); err != nil {
		t.Fatal(err)
	}
	r := adminUsersRouter(store, adminClaims())

	w := doJSON(t, r, "POST", "/api/auth/users/"+itoa(user.ID)+"/reset-password", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	const marker = "/reset-password?token="
	idx := strings.Index(resp.Link, marker)
	if idx < 0 {
		t.Fatalf("link = %q, want a reset-password link", resp.Link)
	}
	tok := resp.Link[idx+len(marker):]
	ti, err := store.ConsumeToken(tok, auth.TokenResetPassword)
	if err != nil {
		t.Fatalf("token from link should be valid: %v", err)
	}
	if ti.UserID != user.ID {
		t.Errorf("token user = %d, want %d", ti.UserID, user.ID)
	}
	sessions, _ := store.ListSessions(user.ID, "")
	if len(sessions) != 0 {
		t.Errorf("sessions after reset link = %d, want 0 (all revoked)", len(sessions))
	}
}

func TestAdminUserSessions_ListRevokeRevokeAll(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "alice", "secret123")
	if _, err := store.CreateRefreshToken(user.ID, time.Hour, false, "Safari iPhone", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRefreshToken(user.ID, time.Hour, true, "Chrome Desktop", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	r := adminUsersRouter(store, adminClaims())

	// List: both sessions, with UA/IP, none "current" (the admin isn't them).
	w := doJSON(t, r, "GET", "/api/auth/users/"+itoa(user.ID)+"/sessions", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Sessions []auth.SessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(resp.Sessions))
	}
	for _, s := range resp.Sessions {
		if s.Current {
			t.Error("admin listing must not flag any session as current")
		}
		if s.UserAgent == "" || s.IP == "" {
			t.Errorf("session %s should carry UA/IP, got %q/%q", s.ID, s.UserAgent, s.IP)
		}
	}

	// Revoke one by id.
	w = doJSON(t, r, "DELETE", "/api/auth/users/"+itoa(user.ID)+"/sessions/"+resp.Sessions[0].ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("revoke status = %d", w.Code)
	}
	left, _ := store.ListSessions(user.ID, "")
	if len(left) != 1 {
		t.Fatalf("sessions after single revoke = %d, want 1", len(left))
	}

	// Revoke all.
	w = doJSON(t, r, "DELETE", "/api/auth/users/"+itoa(user.ID)+"/sessions", "")
	if w.Code != http.StatusOK {
		t.Fatalf("revoke-all status = %d", w.Code)
	}
	left, _ = store.ListSessions(user.ID, "")
	if len(left) != 0 {
		t.Fatalf("sessions after revoke-all = %d, want 0", len(left))
	}
}

// TestAdminUserSessions_StoreErrors covers the 500 paths: refresh_tokens is
// dropped out from under the store (users stays intact so the :id lookup still
// resolves), so every session query/exec fails.
func TestAdminUserSessions_StoreErrors(t *testing.T) {
	// Isolated schema: this test DROPs a table to force errors, which would
	// break NewDB's shared per-process schema for every other test.
	pool := dbtest.NewIsolatedDB(t)
	store, err := auth.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	user := createTestUser(t, store, "alice", "secret123")

	// Drop refresh_tokens out from under the store (users stays intact so the
	// :id lookup resolves) so every session query/exec fails → 500.
	if _, err := pool.Exec("DROP TABLE refresh_tokens"); err != nil {
		t.Fatal(err)
	}

	r := adminUsersRouter(store, adminClaims())
	paths := []struct{ method, path string }{
		{"GET", "/api/auth/users/" + itoa(user.ID) + "/sessions"},
		{"DELETE", "/api/auth/users/" + itoa(user.ID) + "/sessions"},
		{"DELETE", "/api/auth/users/" + itoa(user.ID) + "/sessions/abc"},
	}
	for _, p := range paths {
		w := doJSON(t, r, p.method, p.path, "")
		if w.Code != http.StatusInternalServerError {
			t.Errorf("%s %s: status = %d, want 500; body %s", p.method, p.path, w.Code, w.Body.String())
		}
	}
}

// ─── ChangeEmail ────────────────────────────────────────────────────────────

func changeEmailCtx(t *testing.T, store *auth.Store, claims *auth.Claims, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/email", bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	if claims != nil {
		c.Set("auth.claims", claims)
	}
	ChangeEmail(store, nil, "")(c)
	return w
}

func TestChangeEmail_Unauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	w := changeEmailCtx(t, store, nil, `{"password":"x","email":"a@b.co"}`)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestChangeEmail_InvalidFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	user := createTestUser(t, store, "alice", "secret123")
	claims := &auth.Claims{UserID: user.ID, Username: "alice", Role: auth.RoleUser}
	for _, bad := range []string{"", "no-at.com", "a@b", "a b@c.co"} {
		w := changeEmailCtx(t, store, claims, `{"password":"secret123","email":"`+bad+`"}`)
		if w.Code != http.StatusBadRequest {
			t.Errorf("email %q: status = %d, want 400", bad, w.Code)
		}
	}
}

func TestChangeEmail_WrongPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	user := createTestUser(t, store, "alice", "secret123")
	claims := &auth.Claims{UserID: user.ID, Username: "alice", Role: auth.RoleUser}
	w := changeEmailCtx(t, store, claims, `{"password":"wrong","email":"new@test.com"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestChangeEmail_AlreadyInUse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	user := createTestUser(t, store, "alice", "secret123")
	createTestUser(t, store, "bob", "secret123") // bob@test.com
	claims := &auth.Claims{UserID: user.ID, Username: "alice", Role: auth.RoleUser}
	w := changeEmailCtx(t, store, claims, `{"password":"secret123","email":"bob@test.com"}`)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body %s", w.Code, w.Body.String())
	}
}

func TestChangeEmail_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	user := createTestUser(t, store, "alice", "secret123")
	_ = store.SetEmailVerified(user.ID, "")
	claims := &auth.Claims{UserID: user.ID, Username: "alice", Role: auth.RoleUser}

	// Mixed case + spaces are normalized.
	w := changeEmailCtx(t, store, claims, `{"password":"secret123","email":"  New@Test.COM "}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	got, err := store.GetUserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "new@test.com" {
		t.Errorf("email = %q, want new@test.com", got.Email)
	}
	if got.EmailVerified {
		t.Error("emailVerified must reset to false after a change")
	}
}

// ─── ChangePassword with session revocation ────────────────────────────────

func TestChangePassword_RevokesOtherSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	user := createTestUser(t, store, "alice", "secret123")
	keep, err := store.CreateRefreshToken(user.ID, time.Hour, false, "this device", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRefreshToken(user.ID, time.Hour, false, "other device", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"current":"secret123","new":"newpass1","refresh":"` + keep + `"}`
	c.Request = httptest.NewRequest("POST", "/api/auth/password", bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("auth.claims", &auth.Claims{UserID: user.ID, Username: "alice", Role: auth.RoleUser})
	ChangePassword(store)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Revoked int `json:"revoked"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Revoked != 1 {
		t.Errorf("revoked = %d, want 1", resp.Revoked)
	}
	sessions, _ := store.ListSessions(user.ID, keep)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want only the kept one", len(sessions))
	}
	if !sessions[0].Current {
		t.Error("the surviving session should be the caller's own")
	}
}

func TestChangePassword_NoRefreshKeepsSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	user := createTestUser(t, store, "alice", "secret123")
	if _, err := store.CreateRefreshToken(user.ID, time.Hour, false, "", ""); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"current":"secret123","new":"newpass1"}`
	c.Request = httptest.NewRequest("POST", "/api/auth/password", bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("auth.claims", &auth.Claims{UserID: user.ID, Username: "alice", Role: auth.RoleUser})
	ChangePassword(store)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	sessions, _ := store.ListSessions(user.ID, "")
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1 (untouched without refresh)", len(sessions))
	}
}
