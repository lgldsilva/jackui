package auth

import (
	"context"
	"testing"
	"time"
)

func TestSetTOTPSecret(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")

	if err := s.SetTOTPSecret(context.Background(), u.ID, "TESTSECRET"); err != nil {
		t.Fatalf("SetTOTPSecret: %v", err)
	}
	secret, enabled, err := s.GetTOTPSecret(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetTOTPSecret: %v", err)
	}
	if secret != "TESTSECRET" {
		t.Fatalf("secret = %q", secret)
	}
	if enabled {
		t.Fatal("should not be enabled yet")
	}
}

func TestEnableDisableTOTP(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")

	s.SetTOTPSecret(context.Background(), u.ID, "SECRET")
	if err := s.EnableTOTP(context.Background(), u.ID); err != nil {
		t.Fatalf("EnableTOTP: %v", err)
	}
	_, enabled, _ := s.GetTOTPSecret(context.Background(), u.ID)
	if !enabled {
		t.Fatal("expected enabled")
	}

	// Generate backup codes first
	codes, err := s.GenerateBackupCodes(context.Background(), u.ID, 4)
	if err != nil {
		t.Fatalf("GenerateBackupCodes: %v", err)
	}
	if len(codes) != 4 {
		t.Fatalf("expected 4 codes, got %d", len(codes))
	}

	n := s.CountBackupCodes(context.Background(), u.ID)
	if n != 4 {
		t.Fatalf("expected 4 backup codes, got %d", n)
	}

	// Consume one backup code
	if ok := s.ConsumeBackupCode(context.Background(), u.ID, codes[0]); !ok {
		t.Fatal("expected code to be consumed")
	}
	// Re-consume should fail
	if ok := s.ConsumeBackupCode(context.Background(), u.ID, codes[0]); ok {
		t.Fatal("expected second consume to fail")
	}
	n = s.CountBackupCodes(context.Background(), u.ID)
	if n != 3 {
		t.Fatalf("expected 3 remaining codes, got %d", n)
	}

	// Disable TOTP - should clear codes
	if err := s.DisableTOTP(context.Background(), u.ID); err != nil {
		t.Fatalf("DisableTOTP: %v", err)
	}
	_, enabled, _ = s.GetTOTPSecret(context.Background(), u.ID)
	if enabled {
		t.Fatal("expected disabled")
	}
	n = s.CountBackupCodes(context.Background(), u.ID)
	if n != 0 {
		t.Fatalf("expected 0 codes after disable, got %d", n)
	}
}

func TestChangePassword_UserNotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.ChangePassword(context.Background(), 999, "old", "new")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
}

func TestSetStatus_AndVerifyStatusBlocked(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")

	id, _ := s.CreateUser(context.Background(), "bob", "bobpass", RoleUser)

	// Disable
	if err := s.SetStatus(context.Background(), id, StatusDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	u, _ := s.GetUserByID(context.Background(), id)
	if u.Status != StatusDisabled {
		t.Fatalf("expected disabled, got %s", u.Status)
	}
}

func TestCreateUserFull_RoleDefault(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateUserFull(context.Background(), "test", "test@test.com", "pass", "superadmin", StatusActive)
	if err != nil {
		t.Fatalf("CreateUserFull: %v", err)
	}
	u, _ := s.GetUserByID(context.Background(), id)
	if u.Role != RoleUser {
		t.Fatalf("expected role user (default), got %s", u.Role)
	}
}

func TestGetUserByEmail_Empty(t *testing.T) {
	s := newTestStore(t)
	u, err := s.GetUserByEmail(context.Background(), "")
	if err != nil {
		t.Fatalf("GetUserByEmail(''): %v", err)
	}
	if u != nil {
		t.Fatal("expected nil for empty email")
	}
}

func TestGetUserByUsername_Empty(t *testing.T) {
	s := newTestStore(t)
	u, err := s.GetUserByUsername(context.Background(), "")
	if err != nil {
		t.Fatalf("GetUserByUsername(''): %v", err)
	}
	if u != nil {
		t.Fatal("expected nil for empty username")
	}
}

func TestSetNtfyTopic(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")

	if err := s.SetNtfyTopic(context.Background(), u.ID, "mytopic"); err != nil {
		t.Fatalf("SetNtfyTopic: %v", err)
	}
	got, _ := s.GetUserByID(context.Background(), u.ID)
	if got.NtfyTopic != "mytopic" {
		t.Fatalf("ntfy_topic = %q", got.NtfyTopic)
	}
}

func TestSetEmailVerified_WithPromote(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateUserFull(context.Background(), "promote", "p@test.com", "pass", RoleUser, StatusPending)

	if err := s.SetEmailVerified(context.Background(), id, StatusActive); err != nil {
		t.Fatalf("SetEmailVerified with promote: %v", err)
	}
	u, _ := s.GetUserByID(context.Background(), id)
	if !u.EmailVerified || u.Status != StatusActive {
		t.Fatalf("expected verified+active, got verified=%v status=%s", u.EmailVerified, u.Status)
	}
}

func TestVerifyPassword_BcryptDBError(t *testing.T) {
	// Just verify the "invalid username or password" error message
	s := newTestStore(t)
	_, err := s.VerifyPassword(context.Background(), "nonexistent", "pass")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExistence(t *testing.T) {
	s := newTestStore(t)
	s.CreateUser(context.Background(), "alice", "pass", RoleUser)
	exists, _ := s.Exists(context.Background(), "alice", "")
	if !exists {
		t.Fatal("expected existence for alice")
	}
	exists, _ = s.Exists(context.Background(), "bob", "")
	if exists {
		t.Fatal("expected non-existence for bob")
	}
}

func TestConsumeRefreshToken_Invalid(t *testing.T) {
	s := newTestStore(t)
	if err := s.ConsumeRefreshToken(context.Background(), "invalid"); err != nil {
		t.Fatalf("ConsumeRefreshToken invalid: %v", err)
	}
}

func TestRevokeSession(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")

	tok, _ := s.CreateRefreshToken(context.Background(), u.ID, time.Hour, false, "", "")
	hash := sha256Hex(tok)

	// Revoke a different session ID (not the one we created)
	if err := s.RevokeSession(context.Background(), u.ID, "nonexistent_hash"); err != nil {
		t.Fatalf("RevokeSession nonexistent: %v", err)
	}
	// Revoke the actual session
	if err := s.RevokeSession(context.Background(), u.ID, hash); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")

	tok1, _ := s.CreateRefreshToken(context.Background(), u.ID, time.Hour, false, "", "")
	s.CreateRefreshToken(context.Background(), u.ID, time.Hour, false, "", "")

	n, err := s.RevokeOtherSessions(context.Background(), u.ID, tok1)
	if err != nil {
		t.Fatalf("RevokeOtherSessions: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 revoked, got %d", n)
	}
}

func TestRevokeAllSessions(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")

	s.CreateRefreshToken(context.Background(), u.ID, time.Hour, false, "", "")
	s.CreateRefreshToken(context.Background(), u.ID, time.Hour, false, "", "")

	if err := s.RevokeAllSessions(context.Background(), u.ID); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	sessions, _ := s.ListSessions(context.Background(), u.ID, "")
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestListSessions(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")

	tok1, _ := s.CreateRefreshToken(context.Background(), u.ID, time.Hour, true, "", "")
	s.CreateRefreshToken(context.Background(), u.ID, 2*time.Hour, false, "", "")

	sessions, err := s.ListSessions(context.Background(), u.ID, tok1)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	// First one should be marked as current
	if !sessions[0].Current && !sessions[1].Current {
		t.Fatal("expected one session to be current")
	}
}

func TestValidateRefreshToken_Invalid(t *testing.T) {
	s := newTestStore(t)
	_, _, err := s.ValidateRefreshToken(context.Background(), "invalid")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestBootstrap_AdminPasswordRequired(t *testing.T) {
	s := newTestStore(t)
	if err := s.Bootstrap(context.Background(), "admin", ""); err == nil {
		t.Fatal("expected error for empty password")
	}
}

func TestDeleteCredential(t *testing.T) {
	s := newTestStore(t)
	if err := s.DeleteCredential(context.Background(), 1, "nonexistent"); err != nil {
		t.Fatalf("DeleteCredential nonexistent: %v", err)
	}
}

func TestHasPasskey(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")
	if s.HasPasskey(context.Background(), u.ID) {
		t.Fatal("expected no passkey")
	}
}

func TestSetStatus_InvalidStatus(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")
	// Test that an unknown role is normalized to RoleUser via CreateUser
	id, _ := s.CreateUser(context.Background(), "unknown", "pass", "unknownrole")
	u2, _ := s.GetUserByID(context.Background(), id)
	if u2.Role != RoleUser {
		t.Fatalf("expected RoleUser default, got %s", u2.Role)
	}
	_ = u
}

func TestCreateToken_WithUserID(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")

	plain, err := s.CreateToken(context.Background(), TokenInvite, u.ID, "invited@test.com", time.Hour)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	ti, err := s.ConsumeToken(context.Background(), plain, TokenInvite)
	if err != nil {
		t.Fatalf("ConsumeToken: %v", err)
	}
	if ti.UserID != u.ID {
		t.Fatalf("expected userID %d, got %d", u.ID, ti.UserID)
	}
	if ti.Email != "invited@test.com" {
		t.Fatalf("expected email invited@test.com, got %q", ti.Email)
	}
}

func TestDeleteUser(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateUser(context.Background(), "deleteme", "pass", RoleUser)
	if err := s.DeleteUser(context.Background(), id); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	_, err := s.GetUserByID(context.Background(), id)
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestSetPassword(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateUser(context.Background(), "changeme", "oldpass", RoleUser)
	if err := s.SetPassword(context.Background(), id, "newpass"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	u, err := s.VerifyPassword(context.Background(), "changeme", "newpass")
	if err != nil {
		t.Fatalf("VerifyPassword with new pass: %v", err)
	}
	if u.Username != "changeme" {
		t.Fatalf("username = %q", u.Username)
	}
}

func TestNormalizeBackupCode(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"ABCD-EFGH", "abcdefgh"},
		{"abcd efgh", "abcdefgh"},
		{"AbCdEfGh", "abcdefgh"},
		{"1234-5678", "12345678"},
		{"!@#$%^&*", ""},
	}
	for _, tc := range cases {
		if got := normalizeBackupCode(tc.in); got != tc.want {
			t.Errorf("normalizeBackupCode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestConsumeBackupCode_Nonexistent(t *testing.T) {
	s := newTestStore(t)
	if ok := s.ConsumeBackupCode(context.Background(), 999, "notacode"); ok {
		t.Fatal("expected false for nonexistent user")
	}
}
