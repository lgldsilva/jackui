package auth

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestBootstrapAdminIsActiveVerified(t *testing.T) {
	// The bootstrap admin must come out usable: active + email_verified (the
	// new columns default that way so existing installs aren't locked out).
	s := newTestStore(t)
	if err := s.Bootstrap("admin", "secret123"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	u, err := s.VerifyPassword("admin", "secret123")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if u.Status != StatusActive || !u.EmailVerified {
		t.Fatalf("admin should be active+verified, got status=%q verified=%v", u.Status, u.EmailVerified)
	}
}

func TestAuthTokenSingleUse(t *testing.T) {
	s := newTestStore(t)

	plain, err := s.CreateToken(TokenResetPassword, 0, "a@b.com", time.Hour)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	// Wrong purpose → invalid.
	if _, err := s.ConsumeToken(plain, TokenInvite); err == nil {
		t.Fatal("expected purpose mismatch to fail")
	}
	// Right purpose → ok, carries email.
	ti, err := s.ConsumeToken(plain, TokenResetPassword)
	if err != nil {
		t.Fatalf("ConsumeToken: %v", err)
	}
	if ti.Email != "a@b.com" {
		t.Fatalf("email = %q", ti.Email)
	}
	// Single-use: second consume fails.
	if _, err := s.ConsumeToken(plain, TokenResetPassword); err == nil {
		t.Fatal("expected second consume to fail (single-use)")
	}
}

func TestChangePassword(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateUser("bob", "oldpass", RoleUser)
	// Wrong current → rejected.
	if err := s.ChangePassword(id, "WRONG", "newpass"); err == nil {
		t.Fatal("expected wrong current password to fail")
	}
	// Correct current → changed; old no longer works, new does.
	if err := s.ChangePassword(id, "oldpass", "newpass"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := s.VerifyPassword("bob", "oldpass"); err == nil {
		t.Fatal("old password should no longer work")
	}
	if _, err := s.VerifyPassword("bob", "newpass"); err != nil {
		t.Fatalf("new password should work: %v", err)
	}
}

func TestSetStatus(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateUser("carol", "pw", RoleUser)
	if err := s.SetStatus(id, StatusDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	u, _ := s.GetUserByID(id)
	if u.Status != StatusDisabled {
		t.Fatalf("status = %q, want disabled", u.Status)
	}
}

func TestAuthTokenExpired(t *testing.T) {
	s := newTestStore(t)
	plain, _ := s.CreateToken(TokenVerifyEmail, 1, "", -time.Minute) // already expired
	if _, err := s.ConsumeToken(plain, TokenVerifyEmail); err == nil {
		t.Fatal("expected expired token to fail")
	}
}

func TestBootstrapAdmin(t *testing.T) {
	s := newTestStore(t)
	if err := s.Bootstrap("admin", "secret123"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	// Subsequent bootstrap should not error or duplicate
	if err := s.Bootstrap("admin", "secret123"); err != nil {
		t.Fatalf("re-Bootstrap: %v", err)
	}
	users, _ := s.ListUsers()
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].Role != RoleAdmin {
		t.Errorf("admin role: got %s", users[0].Role)
	}
}

func TestVerifyPasswordCorrectAndWrong(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "correctpass")

	u, err := s.VerifyPassword("admin", "correctpass")
	if err != nil {
		t.Fatalf("verify correct: %v", err)
	}
	if u.Username != "admin" {
		t.Errorf("username: got %s", u.Username)
	}

	if _, err := s.VerifyPassword("admin", "wrongpass"); err == nil {
		t.Error("expected error on wrong password")
	}
	if _, err := s.VerifyPassword("nonexistent", "x"); err == nil {
		t.Error("expected error on nonexistent user")
	}
}

func TestCreateUserAndList(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")

	id, err := s.CreateUser("bob", "bobpass", RoleUser)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if id == 0 {
		t.Fatal("expected positive ID")
	}

	users, _ := s.ListUsers()
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
}

func TestRefreshTokenLifecycle(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")

	token, err := s.CreateRefreshToken(u.ID, 1*time.Hour, false)
	if err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	got, remember, err := s.ValidateRefreshToken(token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("user ID: got %d want %d", got.ID, u.ID)
	}
	if remember {
		t.Error("expected remember=false")
	}

	// Consume → can't validate again
	if err := s.ConsumeRefreshToken(token); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if _, _, err := s.ValidateRefreshToken(token); err == nil {
		t.Error("expected error after consume")
	}
}

func TestRefreshRememberMeFlag(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")

	token, _ := s.CreateRefreshToken(u.ID, 30*24*time.Hour, true)
	_, remember, _ := s.ValidateRefreshToken(token)
	if !remember {
		t.Error("remember flag not persisted")
	}
}

func TestRefreshExpired(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")

	// Negative TTL → immediately expired
	token, _ := s.CreateRefreshToken(u.ID, -1*time.Hour, false)
	if _, _, err := s.ValidateRefreshToken(token); err == nil {
		t.Error("expected error for expired token")
	}
}

func TestCleanupExpired(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	u, _ := s.VerifyPassword("admin", "x")

	s.CreateRefreshToken(u.ID, -1*time.Hour, false)  // expired
	s.CreateRefreshToken(u.ID, 1*time.Hour, false)   // valid

	if err := s.CleanupExpired(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	// 1 row should remain
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM refresh_tokens").Scan(&n)
	if n != 1 {
		t.Errorf("expected 1 token left, got %d", n)
	}
}

func TestDeleteUserCascadesRefreshTokens(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap("admin", "x")
	id, _ := s.CreateUser("bob", "p", RoleUser)
	s.CreateRefreshToken(id, 1*time.Hour, false)

	if err := s.DeleteUser(id); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM refresh_tokens WHERE user_id = ?", id).Scan(&n)
	if n != 0 {
		t.Errorf("expected 0 refresh tokens (cascade), got %d", n)
	}
}
