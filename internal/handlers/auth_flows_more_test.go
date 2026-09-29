package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/auth"
)

// A replayed refresh token past the grace window is treated as theft: 401 AND
// every session of the user is revoked.
func TestRefreshOutcomeOK_ReuseRevokesAllSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	user := createTestUser(t, store, "victim", "password1")
	if _, err := store.CreateRefreshToken(user.ID, time.Hour, false, "ua", "ip"); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)

	if refreshOutcomeOK(c, store, user, auth.RefreshReuse) {
		t.Fatal("reuse must not be OK")
	}
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "invalid refresh token") {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	sessions, _ := store.ListSessions(user.ID, "")
	if len(sessions) != 0 {
		t.Errorf("sessions after reuse = %d, want 0 (all revoked)", len(sessions))
	}
}

func TestRevokeSession_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	user := createTestUser(t, store, "revoker", "password1")
	if _, err := store.CreateRefreshToken(user.ID, time.Hour, false, "ua", "ip"); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.ListSessions(user.ID, "")
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %d err=%v, want 1", len(sessions), err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/auth/sessions/"+sessions[0].ID, nil)
	c.Params = gin.Params{{Key: "id", Value: sessions[0].ID}}
	setAuth(c, user.ID, false)

	RevokeSession(store)(c)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "session terminated") {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	if left, _ := store.ListSessions(user.ID, ""); len(left) != 0 {
		t.Errorf("sessions left = %d, want 0", len(left))
	}
}

func TestRevokeOtherSessions_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	user := createTestUser(t, store, "multi", "password1")
	keep, err := store.CreateRefreshToken(user.ID, time.Hour, false, "phone", "ip1")
	if err != nil {
		t.Fatal(err)
	}
	for _, ua := range []string{"laptop", "tv"} {
		if _, err := store.CreateRefreshToken(user.ID, time.Hour, false, ua, "ip2"); err != nil {
			t.Fatal(err)
		}
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/sessions/revoke-others", strings.NewReader(`{"refresh":"`+keep+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, user.ID, false)

	RevokeOtherSessions(store)(c)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"revoked":2`) {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	left, _ := store.ListSessions(user.ID, keep)
	if len(left) != 1 || !left[0].Current {
		t.Errorf("sessions left = %+v, want only the current one", left)
	}
}

// A valid TOTP against the pending secret enables MFA and hands back the
// one-time backup codes.
func TestMFAEnrollVerify_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	user := createTestUser(t, store, "mfauser", "password1")
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTOTPSecret(user.ID, secret); err != nil {
		t.Fatal(err)
	}
	code := totpCodeAt(secret, uint64(time.Now().Unix()/30))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", strings.NewReader(`{"code":"`+code+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, user.ID, false)

	MFAEnrollVerify(store)(c)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "MFA enabled") || !strings.Contains(w.Body.String(), "backupCodes") {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	if _, enabled, _ := store.GetTOTPSecret(user.ID); !enabled {
		t.Error("TOTP should be enabled after a successful verify")
	}
}

// The pending login points at a user that vanished between callback and
// exchange → the code is consumed and the exchange is refused.
func TestOAuthExchange_UserGoneInvalidatesSession(t *testing.T) {
	h := testOAuthHandlers(t, newOAuthTestStore(t), nil)
	code, err := h.flow.SavePending(424242, false)
	if err != nil {
		t.Fatal(err)
	}

	w := callOAuth(h.Exchange(), http.MethodPost, "/exchange", `{"code":"`+code+`"}`)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "invalid login session") {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	if _, _, ok := h.flow.PeekPending(code); ok {
		t.Error("pending code must be consumed on a dead user")
	}
}

// Link mode needs a public base URL to build the reset link; without one the
// admin gets a clear 500 rather than a broken relative link.
func TestAdminResetPassword_LinkModeNoBaseURL(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "nolink", "oldpass1")
	r := adminUsersRouterWithBaseURL(store, adminClaims(), "")

	w := doJSON(t, r, "POST", "/api/auth/users/"+itoa(user.ID)+"/reset-password", `{}`)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "public base URL not configured") {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
}

// Rerun-incomplete shares the single-run tracker with the full benchmark: a
// second run while one is in flight is refused with 409.
func TestRunAIBenchmarkIncomplete_RejectsConcurrentRun(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tracker := NewBenchmarkRunTracker()
	if !tracker.start(func() {}) {
		t.Fatal("setup: start should succeed on a fresh tracker")
	}
	defer tracker.finish()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/ai/benchmark/incomplete", nil)
	RunAIBenchmarkIncomplete(&ai.Client{}, &ai.BenchmarkStore{}, tracker)(c)

	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "a benchmark is already running") {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
}
