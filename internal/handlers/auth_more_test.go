package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
)

func newAuthStore(t *testing.T) *auth.Store {
	t.Helper()
	store, err := auth.New(t.TempDir() + "/auth.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store
}

func createTestUser(t *testing.T, store *auth.Store, username, password string) *auth.User {
	t.Helper()
	id, err := store.CreateUserFull(username, username+"@test.com", password, auth.RoleUser, auth.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.GetUserByID(id)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func TestLogin_LockedOut(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)
	lockout := auth.NewLockout(3, time.Minute)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"username":"test","password":"wrong"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	Login(store, tm, lockout)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	createTestUser(t, store, "testuser", "correctpass")
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)
	lockout := auth.NewLockout(3, time.Minute)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"username":"testuser","password":"wrongpass"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	Login(store, tm, lockout)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

func TestLogin_Successful(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	createTestUser(t, store, "testuser", "correctpass")
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"username":"testuser","password":"correctpass"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	Login(store, tm, nil)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["access"] == nil || resp["access"] == "" {
		t.Error("expected non-empty access token")
	}
	if resp["refresh"] == nil || resp["refresh"] == "" {
		t.Error("expected non-empty refresh token")
	}
}

func TestLogin_WithRemember(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	createTestUser(t, store, "testuser", "correctpass")
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"username":"testuser","password":"correctpass","remember":true}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	Login(store, tm, nil)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestRespondIfLocked_Locked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lockout := auth.NewLockout(1, time.Minute)
	lockout.Fail("testuser")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/login", nil)

	result := respondIfLocked(c, lockout, "testuser")

	if !result {
		t.Error("expected true (locked)")
	}
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429; body: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}

func TestRespondIfLocked_NotLocked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lockout := auth.NewLockout(3, time.Minute)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/login", nil)

	result := respondIfLocked(c, lockout, "testuser")

	if result {
		t.Error("expected false (not locked)")
	}
}

func TestRespondIfInactive_Pending(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/login", nil)

	result := respondIfInactive(c, auth.StatusPending)

	if !result {
		t.Error("expected true (pending)")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
}

func TestRespondIfInactive_Disabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/login", nil)

	result := respondIfInactive(c, auth.StatusDisabled)

	if !result {
		t.Error("expected true (disabled)")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
}

func TestRespondIfInactive_Active(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/login", nil)

	result := respondIfInactive(c, auth.StatusActive)

	if result {
		t.Error("expected false (active)")
	}
}

func TestIssueTokens(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "testuser", "pass")
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)

	resp, err := issueTokens(store, tm, user, false)
	if err != nil {
		t.Fatalf("issueTokens failed: %v", err)
	}

	if resp.Access == "" {
		t.Error("expected non-empty access token")
	}
	if resp.Refresh == "" {
		t.Error("expected non-empty refresh token")
	}
	if resp.ExpiresAt.IsZero() {
		t.Error("expected non-zero expiresAt")
	}
	if resp.User == nil {
		t.Error("expected user in response")
	}
}

func TestIssueTokens_Remember(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "testuser", "pass")
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)

	resp, err := issueTokens(store, tm, user, true)
	if err != nil {
		t.Fatalf("issueTokens failed: %v", err)
	}

	if resp.Refresh == "" {
		t.Error("expected non-empty refresh token")
	}
}

func TestRefresh_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"refresh":"invalid-token"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/refresh", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	Refresh(store, tm)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

func TestRefresh_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	user := createTestUser(t, store, "testuser", "pass")
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)
	refresh, err := store.CreateRefreshToken(user.ID, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(map[string]string{"refresh": refresh})
	c.Request = httptest.NewRequest("POST", "/api/auth/refresh", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	Refresh(store, tm)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["access"] == nil || resp["access"] == "" {
		t.Error("expected new access token after refresh")
	}
}

func TestMediaToken_UserNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(t.TempDir() + "/auth.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/media-token", nil)
	setAuth(c, 999, false)

	MediaToken(store, tm)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

func TestSetUserStatus_InvalidStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"status":"invalid"}`)
	c.Request = httptest.NewRequest("PATCH", "/api/auth/users/1/status", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	SetUserStatus(store)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestSetUserStatus_MissingBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PATCH", "/api/auth/users/1/status", nil)

	SetUserStatus(store)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestSetUserStatus_CannotDisableSelf(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	createTestUser(t, store, "admin", "pass")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"status":"disabled"}`)
	c.Request = httptest.NewRequest("PATCH", "/api/auth/users/1/status", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	SetUserStatus(store)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (cannot disable self); body: %s", w.Code, w.Body.String())
	}
}

func TestDeleteUser_SelfDeletion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/auth/users/1", nil)
	setAuth(c, 1, true)

	DeleteUser(store)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestDeleteUser_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	createTestUser(t, store, "admin", "pass")
	createTestUser(t, store, "todelete", "pass")

	router := gin.New()
	router.DELETE("/api/auth/users/:id", DeleteUser(store))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/auth/users/2", nil)
	setAuth(c, 1, true)
	router.ServeHTTP(w, c.Request)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	_, err := store.GetUserByID(2)
	if err == nil {
		t.Error("expected user to be deleted")
	}
}

func TestDeleteUser_StoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	store.Close()

	router := gin.New()
	router.DELETE("/api/auth/users/:id", DeleteUser(store))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/auth/users/2", nil)
	setAuth(c, 1, true)
	router.ServeHTTP(w, c.Request)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}

func TestListSessions_WithBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	createTestUser(t, store, "testuser", "pass")
	refresh, err := store.CreateRefreshToken(1, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(map[string]string{"refresh": refresh})
	c.Request = httptest.NewRequest("POST", "/api/auth/sessions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, false)

	ListSessions(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["sessions"] == nil {
		t.Error("expected sessions in response")
	}
}

func TestRevokeSession_StoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	store.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/auth/sessions/1", nil)
	setAuth(c, 1, false)

	RevokeSession(store)(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}

func TestChangePassword_ShortNewPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"current":"old","new":"ab"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/password", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, false)

	ChangePassword(store)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestChangePassword_WrongCurrent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	createTestUser(t, store, "testuser", "correctpass")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"current":"wrong","new":"newpassword"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/password", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, false)

	ChangePassword(store)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestCreateUser_WithBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"username":"newuser","password":"newpass","role":"user"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/users", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	CreateUser(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestListUsers_StoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	store.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/auth/users", nil)

	ListUsers(store)(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}

func TestSetNtfyTopic_EmptyTopic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"topic":""}`)
	c.Request = httptest.NewRequest("POST", "/api/user/ntfy-topic", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, false)

	SetNtfyTopic(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestMFAEnrollStart_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	createTestUser(t, store, "testuser", "pass")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/mfa/enroll", nil)
	setAuth(c, 1, false)

	MFAEnrollStart(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestMFAEnrollVerify_NoCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/mfa/verify", bytes.NewReader([]byte(`{}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, false)

	MFAEnrollVerify(nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestMFABackupCodesRegenerate_NoPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	createTestUser(t, store, "testuser", "pass")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/mfa/backup-codes/regenerate", nil)
	setAuth(c, 1, false)

	MFABackupCodesRegenerate(store)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestMFADisable_WrongPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	createTestUser(t, store, "testuser", "pass")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"password":"wrong"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/mfa/disable", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, false)

	MFADisable(store)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestSetUserStatus_ValidActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	createTestUser(t, store, "admin", "pass")
	createTestUser(t, store, "targetuser", "pass")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = []gin.Param{{Key: "id", Value: "2"}}
	body := []byte(`{"status":"active"}`)
	c.Request = httptest.NewRequest("PATCH", "/api/auth/users/2/status", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	SetUserStatus(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestListSessions_StoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	store.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/sessions", nil)
	setAuth(c, 1, false)

	ListSessions(store)(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}
