package handlers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
)

func TestSetUserStatus_InvalidID_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	params := gin.Params{{Key: "id", Value: "not-an-id"}}
	w := invokeCoverageHandlerWithParams(t, SetUserStatus(&auth.Store{}), http.MethodPatch, "/api/auth/users/not-an-id/status", `{"status":"active"}`, params)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestSetUserStatus_InvalidStatus_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	params := gin.Params{{Key: "id", Value: "1"}}
	w := invokeCoverageHandlerWithParams(t, SetUserStatus(&auth.Store{}), http.MethodPatch, "/api/auth/users/1/status", `{"status":"bogus"}`, params)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestSetUserStatus_SelfDisable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	params := gin.Params{{Key: "id", Value: "1"}}
	claims := &auth.Claims{UserID: 1, Username: "admin"}
	w := invokeCoverageHandlerWithClaims(t, SetUserStatus(&auth.Store{}), http.MethodPatch, "/api/auth/users/1/status", `{"status":"disabled"}`, params, claims)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestCreateUser_MissingFields_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, CreateUser(&auth.Store{}), http.MethodPost, "/api/auth/users", `{"username":"u"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestSetNtfyTopic_MissingBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, SetNtfyTopic(&auth.Store{}), http.MethodPost, "/api/user/ntfy-topic", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestNotifyTest_NoTopic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	w := invokeCoverageHandler(t, NotifyTest(cfg, nil), http.MethodPost, "/api/user/notify-test", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestNtfyBaseURL(t *testing.T) {
	if got := ntfyBaseURL(&config.Config{}); got != "https://ntfy.sh" {
		t.Errorf("default = %q, want https://ntfy.sh", got)
	}
	if got := ntfyBaseURL(&config.Config{Notifications: config.NotificationsConfig{NtfyBaseURL: "https://custom"}}); got != "https://custom" {
		t.Errorf("custom = %q", got)
	}
}

func TestResolveNtfyTopic_NilStore(t *testing.T) {
	topic := resolveNtfyTopic(&config.Config{Notifications: config.NotificationsConfig{NtfyDefaultTopic: "default"}}, nil, nil)
	if topic != "default" {
		t.Errorf("nil store = %q, want default", topic)
	}
}

func TestDeleteUser_InvalidID_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	params := gin.Params{{Key: "id", Value: "not-an-id"}}
	w := invokeCoverageHandlerWithParams(t, DeleteUser(&auth.Store{}), http.MethodDelete, "/api/auth/users/not-an-id", "", params)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDeleteUser_SelfDelete_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	params := gin.Params{{Key: "id", Value: "1"}}
	claims := &auth.Claims{UserID: 1, Username: "admin"}
	w := invokeCoverageHandlerWithClaims(t, DeleteUser(&auth.Store{}), http.MethodDelete, "/api/auth/users/1", "", params, claims)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
