package handlers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
)

func TestRegister_InvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, Register(&auth.Store{}, nil, "https://example.com"), http.MethodPost, "/api/auth/register", "{")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestRegister_MissingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, Register(&auth.Store{}, nil, "https://example.com"), http.MethodPost, "/api/auth/register", `{"username":"u","email":"e"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestVerifyEmail_MissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, VerifyEmail(&auth.Store{}), http.MethodPost, "/api/auth/verify-email", `{"token":""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestReset_MissingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, Reset(&auth.Store{}), http.MethodPost, "/api/auth/reset", `{"token":"t"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestBaseURL(t *testing.T) {
	if got := baseURL(nil, "https://example.com/"); got != "https://example.com" {
		t.Errorf("trailing slash = %q", got)
	}
	if got := baseURL(nil, "  https://example.com  "); got != "https://example.com" {
		t.Errorf("whitespace = %q", got)
	}
	if got := baseURL(nil, ""); got != "" {
		t.Errorf("empty = %q", got)
	}
}

func TestNotify_NoSMTP(t *testing.T) {
	// Best-effort: must not panic with a nil mailer.
	notify(nil, "to@example.com", "Subject", "Intro", "https://link")
}

func TestInvite_NoBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, Invite(&auth.Store{}, nil, ""), http.MethodPost, "/api/auth/invite", `{"email":"a@b.com"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}
