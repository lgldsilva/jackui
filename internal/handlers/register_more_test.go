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

func TestRegisterHandler_InvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/register", bytes.NewReader([]byte(`not json`)))
	c.Request.Header.Set("Content-Type", "application/json")

	registerHandler(c, store, nil, "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestRegisterHandler_MissingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"username":"","email":"","password":"ab"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/register", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	registerHandler(c, store, nil, "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestRegisterHandler_DuplicateUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.CreateUserFull("existing", "existing@test.com", "password123", auth.RoleUser, auth.StatusActive)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"username":"existing","email":"other@test.com","password":"password123"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/register", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	registerHandler(c, store, nil, "")

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
}

func TestRegisterHandler_DuplicateEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.CreateUserFull("other", "dup@test.com", "password123", auth.RoleUser, auth.StatusActive)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"username":"newuser","email":"dup@test.com","password":"password123"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/register", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	registerHandler(c, store, nil, "")

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
}

func TestRegisterHandler_SuccessPending(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"username":"newuser","email":"new@test.com","password":"password123"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/register", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	registerHandler(c, store, nil, "")

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["invited"] != false {
		t.Errorf("invited = %v, want false", resp["invited"])
	}
	if resp["status"] != "pending" {
		t.Errorf("status = %v, want 'pending'", resp["status"])
	}
}

func TestRegisterHandler_SuccessInvited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	inviteToken, err := store.CreateToken(auth.TokenInvite, 0, "invited@test.com", inviteTTL)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"username":"inviteduser","email":"invited@test.com","password":"password123","invite":"` + inviteToken + `"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/register", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	registerHandler(c, store, nil, "")

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["invited"] != true {
		t.Errorf("invited = %v, want true", resp["invited"])
	}
	if resp["status"] != "active" {
		t.Errorf("status = %v, want 'active'", resp["status"])
	}
}

func TestRegisterHandler_InvalidInvite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"username":"newuser","email":"new@test.com","password":"password123","invite":"badtoken"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/register", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	registerHandler(c, store, nil, "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestResolveInviteStatus_Empty(t *testing.T) {
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	status, invited, ierr := resolveInviteStatus(store, "")
	if ierr != nil {
		t.Fatal(ierr)
	}
	if status != auth.StatusPending {
		t.Errorf("status = %s, want pending", status)
	}
	if invited {
		t.Error("invited should be false")
	}
}

func TestResolveInviteStatus_Invalid(t *testing.T) {
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	_, _, ierr := resolveInviteStatus(store, "invalid-token")
	if ierr == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestVerifyEmail_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"token":"invalid"}`)
	c.Request = httptest.NewRequest("POST", "/api/auth/verify-email", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	VerifyEmail(store)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestForgot_NeutralResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/forgot", nil)

	Forgot(store, nil, "")(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestReset_NoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/reset", nil)

	Reset(store)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestInvite_GeneratesLink(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/invite", bytes.NewReader([]byte(`{}`)))
	c.Request.Header.Set("Content-Type", "application/json")

	Invite(store, nil, "")(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	link, _ := resp["link"].(string)
	if !strings.HasPrefix(link, "http") {
		t.Errorf("link = %q, want http prefix", link)
	}
}

func TestBaseURL_EmptyConfigEmptyRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	got := baseURL(c, "")
	if got != "http://"+c.Request.Host {
		t.Errorf("got %q, want http://...", got)
	}
}
