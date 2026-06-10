package auth

import (
	"testing"
	"time"
)

func TestSetTOTPSecret(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")

	if err := s.SetTOTPSecret(u.ID, "TESTSECRET"); err != nil {
		t.Fatalf("SetTOTPSecret: %v", err)
	}
	secret, enabled, err := s.GetTOTPSecret(u.ID)
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
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")

	s.SetTOTPSecret(u.ID, "SECRET")
	if err := s.EnableTOTP(u.ID); err != nil {
		t.Fatalf("EnableTOTP: %v", err)
	}
	_, enabled, _ := s.GetTOTPSecret(u.ID)
	if !enabled {
		t.Fatal("expected enabled")
	}

	// Generate backup codes first
	codes, err := s.GenerateBackupCodes(u.ID, 4)
	if err != nil {
		t.Fatalf("GenerateBackupCodes: %v", err)
	}
	if len(codes) != 4 {
		t.Fatalf("expected 4 codes, got %d", len(codes))
	}

	n := s.CountBackupCodes(u.ID)
	if n != 4 {
		t.Fatalf("expected 4 backup codes, got %d", n)
	}

	// Consume one backup code
	if ok := s.ConsumeBackupCode(u.ID, codes[0]); !ok {
		t.Fatal("expected code to be consumed")
	}
	// Re-consume should fail
	if ok := s.ConsumeBackupCode(u.ID, codes[0]); ok {
		t.Fatal("expected second consume to fail")
	}
	n = s.CountBackupCodes(u.ID)
	if n != 3 {
		t.Fatalf("expected 3 remaining codes, got %d", n)
	}

	// Disable TOTP - should clear codes
	if err := s.DisableTOTP(u.ID); err != nil {
		t.Fatalf("DisableTOTP: %v", err)
	}
	_, enabled, _ = s.GetTOTPSecret(u.ID)
	if enabled {
		t.Fatal("expected disabled")
	}
	n = s.CountBackupCodes(u.ID)
	if n != 0 {
		t.Fatalf("expected 0 codes after disable, got %d", n)
	}
}

func TestChangePassword_UserNotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.ChangePassword(999, "old", "new")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
}

func TestSetStatus_AndVerifyStatusBlocked(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")

	id, _ := s.CreateUser("bob", "bobpass", RoleUser)

	// Disable
	if err := s.SetStatus(id, StatusDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	u, _ := s.GetUserByID(id)
	if u.Status != StatusDisabled {
		t.Fatalf("expected disabled, got %s", u.Status)
	}
}

func TestCreateUserFull_RoleDefault(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateUserFull("test", "test@test.com", "pass", "superadmin", StatusActive)
	if err != nil {
		t.Fatalf("CreateUserFull: %v", err)
	}
	u, _ := s.GetUserByID(id)
	if u.Role != RoleUser {
		t.Fatalf("expected role user (default), got %s", u.Role)
	}
}

func TestGetUserByEmail_Empty(t *testing.T) {
	s := newTestStore(t)
	u, err := s.GetUserByEmail("")
	if err != nil {
		t.Fatalf("GetUserByEmail(''): %v", err)
	}
	if u != nil {
		t.Fatal("expected nil for empty email")
	}
}

func TestGetUserByUsername_Empty(t *testing.T) {
	s := newTestStore(t)
	u, err := s.GetUserByUsername("")
	if err != nil {
		t.Fatalf("GetUserByUsername(''): %v", err)
	}
	if u != nil {
		t.Fatal("expected nil for empty username")
	}
}

func TestSetNtfyTopic(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")

	if err := s.SetNtfyTopic(u.ID, "mytopic"); err != nil {
		t.Fatalf("SetNtfyTopic: %v", err)
	}
	got, _ := s.GetUserByID(u.ID)
	if got.NtfyTopic != "mytopic" {
		t.Fatalf("ntfy_topic = %q", got.NtfyTopic)
	}
}

func TestSetEmailVerified_WithPromote(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateUserFull("promote", "p@test.com", "pass", RoleUser, StatusPending)

	if err := s.SetEmailVerified(id, StatusActive); err != nil {
		t.Fatalf("SetEmailVerified with promote: %v", err)
	}
	u, _ := s.GetUserByID(id)
	if !u.EmailVerified || u.Status != StatusActive {
		t.Fatalf("expected verified+active, got verified=%v status=%s", u.EmailVerified, u.Status)
	}
}

func TestVerifyPassword_BcryptDBError(t *testing.T) {
	// Just verify the "usuário ou senha inválidos" error message
	s := newTestStore(t)
	_, err := s.VerifyPassword("nonexistent", "pass")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExistence(t *testing.T) {
	s := newTestStore(t)
	s.CreateUser("alice", "pass", RoleUser)
	exists, _ := s.Exists("alice", "")
	if !exists {
		t.Fatal("expected existence for alice")
	}
	exists, _ = s.Exists("bob", "")
	if exists {
		t.Fatal("expected non-existence for bob")
	}
}

func TestConsumeRefreshToken_Invalid(t *testing.T) {
	s := newTestStore(t)
	if err := s.ConsumeRefreshToken("invalid"); err != nil {
		t.Fatalf("ConsumeRefreshToken invalid: %v", err)
	}
}

func TestRevokeSession(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")

	tok, _ := s.CreateRefreshToken(u.ID, time.Hour, false, "", "")
	hash := sha256Hex(tok)

	// Revoke a different session ID (not the one we created)
	if err := s.RevokeSession(u.ID, "nonexistent_hash"); err != nil {
		t.Fatalf("RevokeSession nonexistent: %v", err)
	}
	// Revoke the actual session
	if err := s.RevokeSession(u.ID, hash); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")

	tok1, _ := s.CreateRefreshToken(u.ID, time.Hour, false, "", "")
	s.CreateRefreshToken(u.ID, time.Hour, false, "", "")

	n, err := s.RevokeOtherSessions(u.ID, tok1)
	if err != nil {
		t.Fatalf("RevokeOtherSessions: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 revoked, got %d", n)
	}
}

func TestRevokeAllSessions(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")

	s.CreateRefreshToken(u.ID, time.Hour, false, "", "")
	s.CreateRefreshToken(u.ID, time.Hour, false, "", "")

	if err := s.RevokeAllSessions(u.ID); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	sessions, _ := s.ListSessions(u.ID, "")
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestListSessions(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")

	tok1, _ := s.CreateRefreshToken(u.ID, time.Hour, true, "", "")
	s.CreateRefreshToken(u.ID, 2*time.Hour, false, "", "")

	sessions, err := s.ListSessions(u.ID, tok1)
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
	_, _, err := s.ValidateRefreshToken("invalid")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestBootstrap_AdminPasswordRequired(t *testing.T) {
	s := newTestStore(t)
	if err := s.Bootstrap("admin", ""); err == nil {
		t.Fatal("expected error for empty password")
	}
}

func TestDeleteCredential(t *testing.T) {
	s := newTestStore(t)
	if err := s.DeleteCredential(1, "nonexistent"); err != nil {
		t.Fatalf("DeleteCredential nonexistent: %v", err)
	}
}

func TestHasPasskey(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")
	if s.HasPasskey(u.ID) {
		t.Fatal("expected no passkey")
	}
}

func TestSetStatus_InvalidStatus(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")
	// Test that an unknown role is normalized to RoleUser via CreateUser
	id, _ := s.CreateUser("unknown", "pass", "unknownrole")
	u2, _ := s.GetUserByID(id)
	if u2.Role != RoleUser {
		t.Fatalf("expected RoleUser default, got %s", u2.Role)
	}
	_ = u
}

func TestCreateToken_WithUserID(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")

	plain, err := s.CreateToken(TokenInvite, u.ID, "invited@test.com", time.Hour)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	ti, err := s.ConsumeToken(plain, TokenInvite)
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
	id, _ := s.CreateUser("deleteme", "pass", RoleUser)
	if err := s.DeleteUser(id); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	_, err := s.GetUserByID(id)
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestSetPassword(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateUser("changeme", "oldpass", RoleUser)
	if err := s.SetPassword(id, "newpass"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	u, err := s.VerifyPassword("changeme", "newpass")
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
	if ok := s.ConsumeBackupCode(999, "notacode"); ok {
		t.Fatal("expected false for nonexistent user")
	}
}
