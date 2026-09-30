package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
)

func doTOTPLogin(t *testing.T, store *auth.Store, tm *auth.TokenManager, totp string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"totpuser","password":"correctpass","totp":"`+totp+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	Login(store, tm, nil)(c)
	return w
}

// A TOTP accepted at login is a credential: observing one code (shoulder-surf,
// phishing proxy) must not grant a second login within its validity window.
// The same code may never validate twice; the next window's code is a fresh
// credential and must still work.
func TestLogin_TOTPCodeIsSingleUse(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "totpuser", "correctpass")
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTOTPSecret(user.ID, secret); err != nil {
		t.Fatal(err)
	}
	if err := store.EnableTOTP(user.ID); err != nil {
		t.Fatal(err)
	}
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)

	now := uint64(time.Now().Unix() / 30)
	code := totpCodeAt(secret, now)

	if w := doTOTPLogin(t, store, tm, code); w.Code != http.StatusOK {
		t.Fatalf("first login with a fresh code: status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	if w := doTOTPLogin(t, store, tm, code); w.Code != http.StatusUnauthorized {
		t.Fatalf("replayed code was accepted: status = %d, want 401; body %s", w.Code, w.Body.String())
	}
	if w := doTOTPLogin(t, store, tm, totpCodeAt(secret, now+1)); w.Code != http.StatusOK {
		t.Fatalf("next-window code rejected: status = %d, want 200; body %s", w.Code, w.Body.String())
	}
}
