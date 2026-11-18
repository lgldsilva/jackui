package auth

import (
	"context"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return s
}

func TestBootstrapAdminIsActiveVerified(t *testing.T) {
	// The bootstrap admin must come out usable: active + email_verified (the
	// new columns default that way so existing installs aren't locked out).
	s := newTestStore(t)
	if err := s.Bootstrap(context.Background(), "admin", "secret123"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	u, err := s.VerifyPassword(context.Background(), "admin", "secret123")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if u.Status != StatusActive || !u.EmailVerified {
		t.Fatalf("admin should be active+verified, got status=%q verified=%v", u.Status, u.EmailVerified)
	}
}

func TestAuthTokenSingleUse(t *testing.T) {
	s := newTestStore(t)

	plain, err := s.CreateToken(context.Background(), TokenResetPassword, 0, "a@b.com", time.Hour)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	// Wrong purpose → invalid.
	if _, err := s.ConsumeToken(context.Background(), plain, TokenInvite); err == nil {
		t.Fatal("expected purpose mismatch to fail")
	}
	// Right purpose → ok, carries email.
	ti, err := s.ConsumeToken(context.Background(), plain, TokenResetPassword)
	if err != nil {
		t.Fatalf("ConsumeToken: %v", err)
	}
	if ti.Email != "a@b.com" {
		t.Fatalf("email = %q", ti.Email)
	}
	// Single-use: second consume fails.
	if _, err := s.ConsumeToken(context.Background(), plain, TokenResetPassword); err == nil {
		t.Fatal("expected second consume to fail (single-use)")
	}
}

func TestRegistrationStoreFlow(t *testing.T) {
	s := newTestStore(t)
	// Self-register → pending, unverified.
	id, err := s.CreateUserFull(context.Background(), "dave", "dave@x.com", "pw123456", RoleUser, StatusPending)
	if err != nil {
		t.Fatalf("CreateUserFull: %v", err)
	}
	u, _ := s.GetUserByID(context.Background(), id)
	if u.Status != StatusPending || u.EmailVerified || u.Email != "dave@x.com" {
		t.Fatalf("fresh self-register wrong: %+v", u)
	}
	// Uniqueness: username OR email collision.
	if taken, _ := s.Exists(context.Background(), "dave", "other@x.com"); !taken {
		t.Fatal("username collision not detected")
	}
	if taken, _ := s.Exists(context.Background(), "other", "dave@x.com"); !taken {
		t.Fatal("email collision not detected")
	}
	if taken, _ := s.Exists(context.Background(), "nobody", "free@x.com"); taken {
		t.Fatal("false collision")
	}
	// Lookup by email (for password reset).
	if got, _ := s.GetUserByEmail(context.Background(), "dave@x.com"); got == nil || got.ID != id {
		t.Fatal("GetUserByEmail miss")
	}
	// Empty email must never match (existing accounts have email='').
	if got, _ := s.GetUserByEmail(context.Background(), ""); got != nil {
		t.Fatal("empty email should not match")
	}
	// Confirm email.
	if err := s.SetEmailVerified(context.Background(), id, ""); err != nil {
		t.Fatalf("SetEmailVerified: %v", err)
	}
	u, _ = s.GetUserByID(context.Background(), id)
	if !u.EmailVerified {
		t.Fatal("email not marked verified")
	}
}

func TestChangePassword(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateUser(context.Background(), "bob", "oldpass", RoleUser)
	// Wrong current → rejected.
	if err := s.ChangePassword(context.Background(), id, "WRONG", "newpass"); err == nil {
		t.Fatal("expected wrong current password to fail")
	}
	// Correct current → changed; old no longer works, new does.
	if err := s.ChangePassword(context.Background(), id, "oldpass", "newpass"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := s.VerifyPassword(context.Background(), "bob", "oldpass"); err == nil {
		t.Fatal("old password should no longer work")
	}
	if _, err := s.VerifyPassword(context.Background(), "bob", "newpass"); err != nil {
		t.Fatalf("new password should work: %v", err)
	}
}

func TestSetStatus(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateUser(context.Background(), "carol", "pw", RoleUser)
	if err := s.SetStatus(context.Background(), id, StatusDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	u, _ := s.GetUserByID(context.Background(), id)
	if u.Status != StatusDisabled {
		t.Fatalf("status = %q, want disabled", u.Status)
	}
}

func TestAuthTokenExpired(t *testing.T) {
	s := newTestStore(t)
	uid, err := s.CreateUser(context.Background(), "expired-owner", "pass", RoleUser)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// The token must really be persisted (a dangling user_id would fail the FK
	// and make the consume fail for the wrong reason: "invalid token").
	plain, err := s.CreateToken(context.Background(), TokenVerifyEmail, uid, "", -time.Minute) // already expired
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	_, err = s.ConsumeToken(context.Background(), plain, TokenVerifyEmail)
	if err == nil || err.Error() != "token expired" {
		t.Fatalf("ConsumeToken err = %v, want %q", err, "token expired")
	}
}

func TestBootstrapAdmin(t *testing.T) {
	s := newTestStore(t)
	if err := s.Bootstrap(context.Background(), "admin", "secret123"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	// Subsequent bootstrap should not error or duplicate
	if err := s.Bootstrap(context.Background(), "admin", "secret123"); err != nil {
		t.Fatalf("re-Bootstrap: %v", err)
	}
	users, _ := s.ListUsers(context.Background())
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].Role != RoleAdmin {
		t.Errorf("admin role: got %s", users[0].Role)
	}
}

func TestVerifyPasswordCorrectAndWrong(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "correctpass")

	u, err := s.VerifyPassword(context.Background(), "admin", "correctpass")
	if err != nil {
		t.Fatalf("verify correct: %v", err)
	}
	if u.Username != "admin" {
		t.Errorf("username: got %s", u.Username)
	}

	if _, err := s.VerifyPassword(context.Background(), "admin", "wrongpass"); err == nil {
		t.Error("expected error on wrong password")
	}
	if _, err := s.VerifyPassword(context.Background(), "nonexistent", "x"); err == nil {
		t.Error("expected error on nonexistent user")
	}
}

func TestCreateUserAndList(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")

	id, err := s.CreateUser(context.Background(), "bob", "bobpass", RoleUser)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if id == 0 {
		t.Fatal("expected positive ID")
	}

	users, _ := s.ListUsers(context.Background())
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
}

func TestConsumeRefreshTokenOnce_OnlyOneWins(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")
	token, _ := s.CreateRefreshToken(context.Background(), u.ID, 1*time.Hour, false, "", "")

	// First consume removes it and reports the win.
	ok, err := s.ConsumeRefreshTokenOnce(context.Background(), token)
	if err != nil || !ok {
		t.Fatalf("first ConsumeRefreshTokenOnce: ok=%v err=%v, want true,nil", ok, err)
	}
	// Second consume of the SAME token (concurrent refresh / replay) must lose.
	ok, err = s.ConsumeRefreshTokenOnce(context.Background(), token)
	if err != nil || ok {
		t.Fatalf("second ConsumeRefreshTokenOnce: ok=%v err=%v, want false,nil", ok, err)
	}
	// Unknown token also loses (no rows).
	if ok, _ := s.ConsumeRefreshTokenOnce(context.Background(), "nonexistent"); ok {
		t.Error("consuming an unknown token should report false")
	}
}

func TestRefreshTokenLifecycle(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")

	token, err := s.CreateRefreshToken(context.Background(), u.ID, 1*time.Hour, false, "", "")
	if err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	got, remember, err := s.ValidateRefreshToken(context.Background(), token)
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
	if err := s.ConsumeRefreshToken(context.Background(), token); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if _, _, err := s.ValidateRefreshToken(context.Background(), token); err == nil {
		t.Error("expected error after consume")
	}
}

func TestRefreshRememberMeFlag(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")

	token, _ := s.CreateRefreshToken(context.Background(), u.ID, 30*24*time.Hour, true, "", "")
	_, remember, _ := s.ValidateRefreshToken(context.Background(), token)
	if !remember {
		t.Error("remember flag not persisted")
	}
}

func TestRefreshExpired(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")

	// Negative TTL → immediately expired
	token, _ := s.CreateRefreshToken(context.Background(), u.ID, -1*time.Hour, false, "", "")
	if _, _, err := s.ValidateRefreshToken(context.Background(), token); err == nil {
		t.Error("expected error for expired token")
	}
}

func TestCleanupExpired(t *testing.T) {
	s := newTestStore(t)
	s.Bootstrap(context.Background(), "admin", "x")
	u, _ := s.VerifyPassword(context.Background(), "admin", "x")

	s.CreateRefreshToken(context.Background(), u.ID, -1*time.Hour, false, "", "") // expired
	s.CreateRefreshToken(context.Background(), u.ID, 1*time.Hour, false, "", "")  // valid

	if err := s.CleanupExpired(context.Background()); err != nil {
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
	s.Bootstrap(context.Background(), "admin", "x")
	id, _ := s.CreateUser(context.Background(), "bob", "p", RoleUser)
	s.CreateRefreshToken(context.Background(), id, 1*time.Hour, false, "", "")

	if err := s.DeleteUser(context.Background(), id); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM refresh_tokens WHERE user_id = ?", id).Scan(&n)
	if n != 0 {
		t.Errorf("expected 0 refresh tokens (cascade), got %d", n)
	}
}
