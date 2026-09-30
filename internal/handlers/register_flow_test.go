package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbtest"
)

const flowBaseURL = "https://jackui.example.com"

func postFlowJSON(t *testing.T, h gin.HandlerFunc, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	h(c)
	return w
}

// With a public base URL configured, a fresh sign-up issues a verify-email
// token (mailer nil → the link is logged) — the row must carry the token.
func TestRegisterHandler_SendsVerifyEmailWithBaseURL(t *testing.T) {
	pool := dbtest.NewDB(t)
	store, err := auth.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	w := postFlowJSON(t, func(c *gin.Context) { registerHandler(c, store, nil, flowBaseURL) },
		"/api/auth/register", `{"username":"verifyme","email":"verifyme@test.com","password":"password123"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	user, err := store.GetUserByEmail("verifyme@test.com")
	if err != nil || user == nil {
		t.Fatalf("user not created: %v", err)
	}
	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM auth_tokens WHERE purpose = $1 AND user_id = $2`, auth.TokenVerifyEmail, user.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("verify-email tokens = %d, want 1", n)
	}
}

// Invite with an address: the link is emailed (best-effort, logged without
// SMTP) AND still returned so the admin can share it manually.
func TestInvite_WithEmailReturnsLink(t *testing.T) {
	store := newAuthStore(t)
	w := postFlowJSON(t, Invite(store, nil, flowBaseURL), "/api/auth/invite", `{"email":"Friend@Test.com"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Link string `json:"link"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !strings.HasPrefix(resp.Link, flowBaseURL+"/register?invite=") {
		t.Errorf("link = %q, want an invite link on the base URL", resp.Link)
	}
}

func TestVerifyEmail_Success(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "pending", "password1")
	tok, err := store.CreateToken(auth.TokenVerifyEmail, user.ID, user.Email, verifyTTL)
	if err != nil {
		t.Fatal(err)
	}

	w := postFlowJSON(t, VerifyEmail(store), "/api/auth/verify-email", `{"token":"`+tok+`"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "email confirmed") {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	// Single-use: replaying the token is rejected.
	if w := postFlowJSON(t, VerifyEmail(store), "/api/auth/verify-email", `{"token":"`+tok+`"}`); w.Code != http.StatusBadRequest {
		t.Errorf("replayed token status = %d, want 400", w.Code)
	}
}

// Known address + base URL → a reset token is issued; the response stays
// neutral (identical to the unknown-address case).
func TestForgot_KnownEmailIssuesResetToken(t *testing.T) {
	pool := dbtest.NewDB(t)
	store, err := auth.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	user := createTestUser(t, store, "forgetful", "password1")

	w := postFlowJSON(t, Forgot(store, nil, flowBaseURL), "/api/auth/forgot", `{"email":"`+user.Email+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM auth_tokens WHERE purpose = $1 AND user_id = $2`, auth.TokenResetPassword, user.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("reset tokens = %d, want 1", n)
	}
}

func TestReset_Success(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "resetter", "oldpass1")
	tok, err := store.CreateToken(auth.TokenResetPassword, user.ID, user.Email, resetTTL)
	if err != nil {
		t.Fatal(err)
	}

	w := postFlowJSON(t, Reset(store), "/api/auth/reset", `{"token":"`+tok+`","password":"newpass2"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "password reset") {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	if _, err := store.VerifyPassword("resetter", "newpass2"); err != nil {
		t.Errorf("new password should authenticate: %v", err)
	}
	if _, err := store.VerifyPassword("resetter", "oldpass1"); err == nil {
		t.Error("old password must no longer authenticate")
	}
}
