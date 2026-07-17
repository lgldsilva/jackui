package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbtest"
)

func TestPasskeyRegisterBegin_StoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}


	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/passkey/register/begin", nil)
	setAuth(c, 1, false)

	PasskeyRegisterBegin(store, nil)(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyRegisterFinish_StoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}


	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/passkey/register/finish?session=abc", nil)
	setAuth(c, 1, false)

	PasskeyRegisterFinish(store, nil)(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyLoginBegin_EmptyUsername(t *testing.T) {
	gin.SetMode(gin.TestMode)
	wa := &auth.WAManager{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := bytes.NewReader([]byte(`{"username":""}`))
	c.Request = httptest.NewRequest("POST", "/api/auth/passkey/login/begin", body)
	c.Request.Header.Set("Content-Type", "application/json")

	PasskeyLoginBegin(nil, wa)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyLoginBegin_UserNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	wa := &auth.WAManager{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := bytes.NewReader([]byte(`{"username":"nonexistent"}`))
	c.Request = httptest.NewRequest("POST", "/api/auth/passkey/login/begin", body)
	c.Request.Header.Set("Content-Type", "application/json")

	PasskeyLoginBegin(store, wa)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyLoginBegin_NoPasskeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.CreateUserFull("testuser", "test@test.com", "password123", auth.RoleUser, auth.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	wa := &auth.WAManager{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := bytes.NewReader([]byte(`{"username":"testuser"}`))
	c.Request = httptest.NewRequest("POST", "/api/auth/passkey/login/begin", body)
	c.Request.Header.Set("Content-Type", "application/json")

	PasskeyLoginBegin(store, wa)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyLoginFinish_UserNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	wa := &auth.WAManager{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/passkey/login/finish?username=nobody", nil)

	PasskeyLoginFinish(store, nil, wa)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyLoginFinish_PendingStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.CreateUserFull("pendinguser", "pending@test.com", "password123", auth.RoleUser, auth.StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	wa := &auth.WAManager{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/passkey/login/finish?username=pendinguser", nil)

	PasskeyLoginFinish(store, nil, wa)(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyLoginFinish_DisabledStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.CreateUserFull("disableduser", "disabled@test.com", "password123", auth.RoleUser, auth.StatusDisabled)
	if err != nil {
		t.Fatal(err)
	}
	wa := &auth.WAManager{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/passkey/login/finish?username=disableduser", nil)

	PasskeyLoginFinish(store, nil, wa)(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyList_WithCreds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}


	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/auth/passkey", nil)
	setAuth(c, 1, false)

	PasskeyList(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyDelete_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}


	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/auth/passkey/nonexistent", nil)
	setAuth(c, 1, false)

	PasskeyDelete(store)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}
