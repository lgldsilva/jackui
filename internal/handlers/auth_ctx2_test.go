package handlers

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
)

// DeviceToken: once flow.Poll consumed the single-use pairing, everything that
// follows must not depend on the polling client still being connected.
func TestDeviceToken_ClientGone_StillPersistsKey(t *testing.T) {
	store := newAuthStore(t)
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)
	user := createTestUser(t, store, "pair2", "pass")
	flow := auth.NewDeviceFlow()
	deviceCode, p, err := flow.Start(auth.DeviceKeyMeta{DeviceID: "tv10", Name: "TV", IP: "1.2.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := flow.Decide(p.UserCode, user.ID, true); err != nil {
		t.Fatal(err)
	}
	w := callGone(DeviceToken(flow, store, tm), http.MethodPost, "/", `{"deviceCode":"`+deviceCode+`"}`, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	keys, err := store.ListDeviceKeys(context.Background(), user.ID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("device keys = %d, %v; want 1 persisted", len(keys), err)
	}
}

func TestLogout_ClientGone_StillRevokesRefreshToken(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "bye", "pass")
	refresh, _ := store.CreateRefreshToken(context.Background(), user.ID, time.Hour, false, "", "")
	callGone(Logout(store), http.MethodPost, "/api/auth/logout", `{"refresh":"`+refresh+`"}`, nil, nil)
	if _, _, err := store.ValidateRefreshToken(context.Background(), refresh); err == nil {
		t.Fatal("refresh token must be revoked even when the client dropped")
	}
}

func TestSessionRevocations_ClientGone_StillRevoke(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "sess", "pass")
	claims := &auth.Claims{UserID: user.ID, Username: user.Username, Role: auth.RoleUser}
	keep, _ := store.CreateRefreshToken(context.Background(), user.ID, time.Hour, false, "keep", "")
	other, _ := store.CreateRefreshToken(context.Background(), user.ID, time.Hour, false, "other", "")

	callGone(RevokeOtherSessions(store), http.MethodPost, "/", `{"refresh":"`+keep+`"}`, nil, claims)
	if _, _, err := store.ValidateRefreshToken(context.Background(), other); err == nil {
		t.Error("RevokeOtherSessions dropped with the client")
	}
	sessions, _ := store.ListSessions(context.Background(), user.ID, keep)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	callGone(RevokeSession(store), http.MethodDelete, "/", "", gin.Params{{Key: "id", Value: sessions[0].ID}}, claims)
	if _, _, err := store.ValidateRefreshToken(context.Background(), keep); err == nil {
		t.Error("RevokeSession dropped with the client")
	}
}

func TestRevokeDevice_ClientGone_StillRevokes(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "dev", "pass")
	key, id, _ := store.CreateDeviceKey(context.Background(), user.ID, auth.DeviceKeyMeta{DeviceID: "tv"})
	claims := &auth.Claims{UserID: user.ID, Username: user.Username, Role: auth.RoleUser}
	callGone(RevokeDevice(store), http.MethodDelete, "/", "", gin.Params{{Key: "id", Value: strconv.FormatInt(id, 10)}}, claims)
	if _, err := store.LookupDeviceKey(context.Background(), key); err == nil {
		t.Fatal("device key must be revoked even when the client dropped")
	}
}

// Enrolling MFA enables it and mints the recovery codes together; a dropped
// client must not leave MFA on with no backup codes.
func TestEnableMFAWithBackupCodes_ClientGone_IsComplete(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "mfaon", "pass")
	_ = store.SetTOTPSecret(context.Background(), user.ID, "SECRET")
	codes, err := enableMFAWithBackupCodes(goneCtx(), store, user.ID)
	if err != nil || len(codes) != backupCodeCount {
		t.Fatalf("codes = %d, err = %v", len(codes), err)
	}
	if _, enabled, _ := store.GetTOTPSecret(context.Background(), user.ID); !enabled {
		t.Error("MFA not enabled")
	}
	if n := store.CountBackupCodes(context.Background(), user.ID); n != backupCodeCount {
		t.Errorf("backup codes = %d, want %d", n, backupCodeCount)
	}
}
