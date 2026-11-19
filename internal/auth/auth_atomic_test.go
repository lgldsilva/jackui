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
