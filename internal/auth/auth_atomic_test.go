package auth

import (
	"context"
	"errors"
	"testing"
)

// If minting the backup codes fails, enabling TOTP must roll back with it.
func TestEnableTOTPWithBackupCodes_RollsBackOnCodeFailure(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	uid, err := s.CreateUser(ctx, "mfarb", "pw-123456", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SetTOTPSecret(ctx, uid, "SECRET")

	calls := 0
	orig := randomCode
	randomCode = func(n int, alphabet string) (string, error) {
		calls++
		if calls == 2 {
			return "", errors.New("injected")
		}
		return orig(n, alphabet)
	}
	t.Cleanup(func() { randomCode = orig })

	if _, err := s.EnableTOTPWithBackupCodes(ctx, uid, 5); err == nil {
		t.Fatal("expected the injected failure")
	}
	if _, enabled, _ := s.GetTOTPSecret(ctx, uid); enabled {
		t.Error("TOTP must not be enabled when backup codes failed")
	}
	if n := s.CountBackupCodes(ctx, uid); n != 0 {
		t.Errorf("backup codes = %d, want 0", n)
	}
}

func TestEnableTOTPWithBackupCodes_Succeeds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	uid, _ := s.CreateUser(ctx, "mfaok", "pw-123456", RoleUser)
	codes, err := s.EnableTOTPWithBackupCodes(ctx, uid, 5)
	if err != nil || len(codes) != 5 || len(codes[0]) != 9 || codes[0][4] != '-' {
		t.Fatalf("codes = %v, err = %v", codes, err)
	}
	if _, enabled, _ := s.GetTOTPSecret(ctx, uid); !enabled {
		t.Error("TOTP not enabled")
	}
	if n := s.CountBackupCodes(ctx, uid); n != 5 {
		t.Errorf("backup codes = %d, want 5", n)
	}
}

func TestVerifyEmailWithToken_Atomic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	uid, _ := s.CreateUser(ctx, "vt", "pw-123456", RoleUser)
	tok, _ := s.CreateToken(ctx, TokenVerifyEmail, uid, "", 3600e9)
	if err := s.VerifyEmailWithToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyEmailWithToken(ctx, tok); err == nil {
		t.Error("token must be single-use")
	}
}

func TestCreateUserWithInvite_InvalidInvite(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateUserWithInvite(context.Background(), "bogus", "u", "e@x.io", "pw-123456", RoleUser)
	if !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("err = %v, want ErrInviteInvalid", err)
	}
}

// With the request already cancelled, every transactional entry point must
// refuse before touching state (BeginTx fails).
func TestAtomicMethods_CancelledContext(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.VerifyEmailWithToken(ctx, "x"); err == nil {
		t.Error("VerifyEmailWithToken: want error")
	}
	if _, err := s.CreateUserWithInvite(ctx, "x", "u", "e@x.io", "pw-123456", RoleUser); err == nil {
		t.Error("CreateUserWithInvite: want error")
	}
	if _, err := s.EnableTOTPWithBackupCodes(ctx, 1, 5); err == nil {
		t.Error("EnableTOTPWithBackupCodes: want error")
	}
	if _, err := s.GenerateBackupCodes(ctx, 1, 5); err == nil {
		t.Error("GenerateBackupCodes: want error")
	}
}

func TestCreateUserWithInvite_HashFailureAndSuccess(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	long := string(make([]byte, 100))
	if _, err := s.CreateUserWithInvite(ctx, "x", "u", "e@x.io", long, RoleUser); err == nil {
		t.Error("over-long password must be rejected")
	}
	inv, _ := s.CreateToken(ctx, TokenInvite, 0, "", 3600e9)
	id, err := s.CreateUserWithInvite(ctx, inv, "invok", "ok@x.io", "pw-123456", RoleUser)
	if err != nil || id == 0 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	// Same username again with a fresh invite: insert fails, invite survives.
	inv2, _ := s.CreateToken(ctx, TokenInvite, 0, "", 3600e9)
	if _, err := s.CreateUserWithInvite(ctx, inv2, "invok", "o2@x.io", "pw-123456", RoleUser); err == nil {
		t.Fatal("duplicate username must fail")
	}
	if _, err := s.ConsumeToken(ctx, inv2, TokenInvite); err != nil {
		t.Errorf("invite must survive a failed insert: %v", err)
	}
}

func TestGenerateBackupCodes_CodeFailure(t *testing.T) {
	s := newTestStore(t)
	orig := randomCode
	randomCode = func(int, string) (string, error) { return "", errors.New("injected") }
	t.Cleanup(func() { randomCode = orig })
	if _, err := s.GenerateBackupCodes(context.Background(), 1, 3); err == nil {
		t.Fatal("want injected failure")
	}
}
