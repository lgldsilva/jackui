package handlers

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
)

// compromisedAccount seeds a user holding one refresh session and one paired
// device key — the access an attacker keeps unless a password change cuts it.
func compromisedAccount(t *testing.T, store *auth.Store) (user *auth.User, refresh, deviceKey string) {
	t.Helper()
	user = createTestUser(t, store, "victim", "oldpass1")
	refresh, err := store.CreateRefreshToken(user.ID, time.Hour, false, "attacker", "6.6.6.6")
	if err != nil {
		t.Fatal(err)
	}
	deviceKey, _, err = store.CreateDeviceKey(user.ID, auth.DeviceKeyMeta{DeviceID: "tv-evil", Name: "TV", Model: "X", IP: "6.6.6.6"})
	if err != nil {
		t.Fatal(err)
	}
	return user, refresh, deviceKey
}

func assertDeviceKeyRevoked(t *testing.T, store *auth.Store, deviceKey string) {
	t.Helper()
	if _, err := store.LookupDeviceKey(deviceKey); !errors.Is(err, auth.ErrDeviceKeyInvalid) {
		t.Errorf("device key still valid after the password was set (err = %v), want revoked", err)
	}
}

func assertSessionCount(t *testing.T, store *auth.Store, userID, want int) {
	t.Helper()
	sessions, err := store.ListSessions(userID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != want {
		t.Errorf("refresh sessions after the password was set = %d, want %d", len(sessions), want)
	}
}

func changePasswordAs(t *testing.T, store *auth.Store, user *auth.User, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("auth.claims", &auth.Claims{UserID: user.ID, Username: user.Username, Role: auth.RoleUser})
	ChangePassword(store)(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	return w
}

// The public forgot-password flow is how a victim recovers a compromised
// account: it must cut off every refresh session and paired device.
func TestReset_RevokesSessionsAndDeviceKeys(t *testing.T) {
	store := newAuthStore(t)
	user, _, deviceKey := compromisedAccount(t, store)
	tok, err := store.CreateToken(auth.TokenResetPassword, user.ID, user.Email, resetTTL)
	if err != nil {
		t.Fatal(err)
	}

	w := postFlowJSON(t, Reset(store), "/api/auth/reset", `{"token":"`+tok+`","password":"newpass1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	assertSessionCount(t, store, user.ID, 0)
	assertDeviceKeyRevoked(t, store, deviceKey)
}

// Self-service change keeps only the caller's own session; every other
// session and every paired device is revoked.
func TestChangePassword_RevokesDeviceKeysKeepsOwnSession(t *testing.T) {
	store := newAuthStore(t)
	user, _, deviceKey := compromisedAccount(t, store)
	own, err := store.CreateRefreshToken(user.ID, time.Hour, false, "this device", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	changePasswordAs(t, store, user, `{"current":"oldpass1","new":"newpass1","refresh":"`+own+`"}`)

	sessions, _ := store.ListSessions(user.ID, own)
	if len(sessions) != 1 || !sessions[0].Current {
		t.Errorf("sessions = %+v, want only the caller's own", sessions)
	}
	assertDeviceKeyRevoked(t, store, deviceKey)
}

// Without a refresh token the handler can't tell which session is the
// caller's, so it must fail closed and revoke all of them.
func TestChangePassword_NoRefreshRevokesEverything(t *testing.T) {
	store := newAuthStore(t)
	user, _, deviceKey := compromisedAccount(t, store)

	changePasswordAs(t, store, user, `{"current":"oldpass1","new":"newpass1"}`)

	assertSessionCount(t, store, user.ID, 0)
	assertDeviceKeyRevoked(t, store, deviceKey)
}

func TestAdminResetPassword_WithPasswordRevokesDeviceKeys(t *testing.T) {
	store := newAuthStore(t)
	user, _, deviceKey := compromisedAccount(t, store)
	r := adminUsersRouter(store, adminClaims())

	w := doJSON(t, r, "POST", "/api/auth/users/"+itoa(user.ID)+"/reset-password", `{"password":"newpass1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	assertSessionCount(t, store, user.ID, 0)
	assertDeviceKeyRevoked(t, store, deviceKey)
}
