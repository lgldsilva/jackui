package handlers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
)

func TestMFAEnrollStart_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, MFAEnrollStart(&auth.Store{}), http.MethodPost, "/api/auth/mfa/enroll", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestMFAEnrollVerify_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, MFAEnrollVerify(&auth.Store{}), http.MethodPost, "/api/auth/mfa/verify", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestMFAEnrollVerify_MissingCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	claims := &auth.Claims{UserID: 1, Username: "admin"}
	w := invokeCoverageHandlerWithClaims(t, MFAEnrollVerify(&auth.Store{}), http.MethodPost, "/api/auth/mfa/verify", `{"code":""}`, nil, claims)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestMFABackupCodesStatus_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, MFABackupCodesStatus(&auth.Store{}), http.MethodGet, "/api/auth/mfa/backup-codes", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestMFABackupCodesRegenerate_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, MFABackupCodesRegenerate(&auth.Store{}), http.MethodPost, "/api/auth/mfa/backup-codes/regenerate", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestMFADisable_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := invokeCoverageHandler(t, MFADisable(&auth.Store{}), http.MethodPost, "/api/auth/mfa/disable", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}
