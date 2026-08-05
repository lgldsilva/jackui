package handlers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
)

func TestPasskeyRegisterBegin_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, PasskeyRegisterBegin(&auth.Store{}, nil), http.MethodPost, "/api/auth/passkey/register/begin", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestPasskeyRegisterBegin_NotConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	claims := &auth.Claims{UserID: 1, Username: "admin"}
	w := invokeCoverageHandlerWithClaims(t, PasskeyRegisterBegin(&auth.Store{}, nil), http.MethodPost, "/api/auth/passkey/register/begin", "", nil, claims)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestPasskeyRegisterFinish_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, PasskeyRegisterFinish(&auth.Store{}, nil), http.MethodPost, "/api/auth/passkey/register/finish", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestPasskeyRegisterFinish_NotConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	claims := &auth.Claims{UserID: 1, Username: "admin"}
	w := invokeCoverageHandlerWithClaims(t, PasskeyRegisterFinish(&auth.Store{}, nil), http.MethodPost, "/api/auth/passkey/register/finish", "", nil, claims)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestPasskeyLoginBegin_NotConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, PasskeyLoginBegin(&auth.Store{}, nil), http.MethodPost, "/api/auth/passkey/login/begin", `{"username":"u"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestPasskeyLoginBegin_MissingUsername(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, PasskeyLoginBegin(&auth.Store{}, nil), http.MethodPost, "/api/auth/passkey/login/begin", `{"username":""}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestPasskeyLoginFinish_NotConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, PasskeyLoginFinish(&auth.Store{}, nil, nil), http.MethodPost, "/api/auth/passkey/login/finish?username=u&session=s", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestPasskeyList_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, PasskeyList(&auth.Store{}), http.MethodGet, "/api/auth/passkey", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestPasskeyDelete_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	params := gin.Params{{Key: "id", Value: "abc"}}
	w := invokeCoverageHandlerWithParams(t, PasskeyDelete(&auth.Store{}), http.MethodDelete, "/api/auth/passkey/abc", "", params)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}
