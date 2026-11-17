package handlers

import (
	"bytes"
	"database/sql"
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

// brokenRevocationStore returns an auth store on a PRIVATE schema (so the
// schema change can't reach any other test) plus the pool to break it with.
func brokenRevocationStore(t *testing.T) (*auth.Store, *sql.DB) {
	t.Helper()
	pool := dbtest.NewIsolatedDB(t)
	store, err := auth.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	return store, pool
}

// userWithSession seeds a user holding one refresh session.
func userWithSession(t *testing.T, store *auth.Store, username string) *auth.User {
	t.Helper()
	user := createTestUser(t, store, username, "oldpass1")
	if _, err := store.CreateRefreshToken(user.ID, time.Hour, false, "attacker", "6.6.6.6"); err != nil {
		t.Fatal(err)
	}
	return user
}

func dropTable(t *testing.T, pool *sql.DB, table string) {
	t.Helper()
	if _, err := pool.Exec("DROP TABLE " + table + " CASCADE"); err != nil {
		t.Fatal(err)
	}
}

// assertRevocationFailure checks the handler contract when the password was
// set but access could not be revoked: 500 with a JSON error naming what
// survived — never a 200 that tells the victim the account is safe.
func assertRevocationFailure(t *testing.T, w *httptest.ResponseRecorder, wantInError string) {
	t.Helper()
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body %s", w.Code, w.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v; body %s", err, w.Body.String())
	}
	if !strings.Contains(body.Error, wantInError) {
		t.Errorf("error = %q, want it to contain %q", body.Error, wantInError)
	}
}

func assertPasswordIs(t *testing.T, store *auth.Store, username, password string) {
	t.Helper()
	if _, err := store.VerifyPassword(username, password); err != nil {
		t.Errorf("password %q rejected for %s: %v", password, username, err)
	}
}

// Every password-setting handler must surface a failed paired-device
// revocation as a 500 instead of reporting success. The password and the
// session revocation have already been applied at that point.
func TestPasswordHandlers_DeviceKeyRevocationFailureIs500(t *testing.T) {
	store, pool := brokenRevocationStore(t)
	resetUser := userWithSession(t, store, "reset-victim")
	adminUser := userWithSession(t, store, "admin-victim")
	changeUser := userWithSession(t, store, "change-victim")
	tok, err := store.CreateToken(auth.TokenResetPassword, resetUser.ID, resetUser.Email, resetTTL)
	if err != nil {
		t.Fatal(err)
	}
	dropTable(t, pool, "device_keys")
	const want = "paired devices were not revoked"

	t.Run("reset", func(t *testing.T) {
		w := postFlowJSON(t, Reset(store), "/api/auth/reset", `{"token":"`+tok+`","password":"newpass1"}`)
		assertRevocationFailure(t, w, "password set but "+want)
		assertPasswordIs(t, store, resetUser.Username, "newpass1")
		assertSessionCount(t, store, resetUser.ID, 0)
	})

	t.Run("admin reset", func(t *testing.T) {
		r := adminUsersRouter(store, adminClaims())
		w := doJSON(t, r, "POST", "/api/auth/users/"+itoa(adminUser.ID)+"/reset-password", `{"password":"newpass1"}`)
		assertRevocationFailure(t, w, "password set but "+want)
		assertPasswordIs(t, store, adminUser.Username, "newpass1")
		assertSessionCount(t, store, adminUser.ID, 0)
	})

	t.Run("change", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/password",
			bytes.NewReader([]byte(`{"current":"oldpass1","new":"newpass1"}`)))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("auth.claims", &auth.Claims{UserID: changeUser.ID, Username: changeUser.Username, Role: auth.RoleUser})
		ChangePassword(store)(c)
		assertRevocationFailure(t, w, "password changed but "+want)
		assertPasswordIs(t, store, changeUser.Username, "newpass1")
		assertSessionCount(t, store, changeUser.ID, 0)
	})
}

// The helpers stop at the first failure and name what was not revoked, so the
// operator knows which credentials are still live.
func TestRevokeHelpers_WrapTheFailingStep(t *testing.T) {
	store, pool := brokenRevocationStore(t)
	user := userWithSession(t, store, "victim")

	dropTable(t, pool, "device_keys")
	revoked, err := revokeAccessAfterPasswordChange(store, user.ID, "")
	if err == nil || !strings.Contains(err.Error(), "password changed but paired devices were not revoked") {
		t.Errorf("revokeAccessAfterPasswordChange err = %v, want the paired-devices failure", err)
	}
	if revoked != 1 {
		t.Errorf("revoked = %d, want 1 (the session was dropped before the device step failed)", revoked)
	}
	if err := revokeAllAccess(store, user.ID); err == nil || !strings.Contains(err.Error(), "password set but paired devices were not revoked") {
		t.Errorf("revokeAllAccess err = %v, want the paired-devices failure", err)
	}

	dropTable(t, pool, "refresh_tokens")
	revoked, err = revokeAccessAfterPasswordChange(store, user.ID, "")
	if err == nil || !strings.Contains(err.Error(), "password changed but sessions were not revoked") {
		t.Errorf("revokeAccessAfterPasswordChange err = %v, want the sessions failure", err)
	}
	if revoked != 0 {
		t.Errorf("revoked = %d, want 0 when the session step itself failed", revoked)
	}
	if err := revokeAllAccess(store, user.ID); err == nil || !strings.Contains(err.Error(), "password set but sessions were not revoked") {
		t.Errorf("revokeAllAccess err = %v, want the sessions failure", err)
	}
}
