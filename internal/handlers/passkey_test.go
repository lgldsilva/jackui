package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbtest"
)

func TestPasskeyList_NoClaims(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/auth/passkey", nil)

	PasskeyList(nil)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyDelete_NoClaims(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/auth/passkey/abc123", nil)

	PasskeyDelete(nil)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyRegisterBegin_NoClaims(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/passkey/register/begin", nil)

	PasskeyRegisterBegin(nil, nil)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyRegisterBegin_NoWA(t *testing.T) {
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

func TestPasskeyRegisterFinish_NoClaims(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/passkey/register/finish?session=abc", nil)

	PasskeyRegisterFinish(nil, nil)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyRegisterFinish_NoWA(t *testing.T) {
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

func TestPasskeyLoginBegin_NoWA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/passkey/login/begin", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	PasskeyLoginBegin(nil, nil)(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyLoginBegin_NoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	wa := &auth.WAManager{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/passkey/login/begin", nil)

	PasskeyLoginBegin(nil, wa)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestPasskeyLoginFinish_NoWA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/passkey/login/finish", nil)

	PasskeyLoginFinish(nil, nil, nil)(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}
