package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// A context that is already done must stop every store call before it reaches
// Postgres. Before the *Context migration these methods had no ctx at all.
func TestStoreCallsHonourCancelledContext(t *testing.T) {
	s := newTestStore(t)
	live := context.Background()
	uid, err := s.CreateUser(live, "ctxuser", "pw-123456", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := s.CreateRefreshToken(live, uid, time.Hour, false, "ua", "ip")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.CreateToken(live, TokenResetPassword, uid, "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	dk, dkID, err := s.CreateDeviceKey(live, uid, DeviceKeyMeta{DeviceID: "tv", Name: "TV"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.LookupDeviceKey(live, dk)
	if err != nil {
		t.Fatal(err)
	}
	cred := &webauthn.Credential{ID: []byte("cred-1")}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := map[string]func() error{
		"Bootstrap":      func() error { return s.Bootstrap(ctx, "a", "b") },
		"CreateUser":     func() error { _, err := s.CreateUser(ctx, "x", "pw-123456", RoleUser); return err },
		"ChangePassword": func() error { return s.ChangePassword(ctx, uid, "pw-123456", "other-pw") },
		"SetPassword":    func() error { return s.SetPassword(ctx, uid, "other-pw") },
		"SetStatus":      func() error { return s.SetStatus(ctx, uid, StatusDisabled) },
		"CreateUserFull": func() error {
			_, err := s.CreateUserFull(ctx, "y", "y@x", "pw-123456", RoleUser, StatusActive)
			return err
		},
		"UpdateEmail":             func() error { return s.UpdateEmail(ctx, uid, "n@x") },
		"EmailInUse":              func() error { _, err := s.EmailInUse(ctx, "n@x", 0); return err },
		"SetNtfyTopic":            func() error { return s.SetNtfyTopic(ctx, uid, "t") },
		"SetEmailVerified":        func() error { return s.SetEmailVerified(ctx, uid, StatusActive) },
		"GetUserByEmail":          func() error { _, err := s.GetUserByEmail(ctx, "n@x"); return err },
		"Exists":                  func() error { _, err := s.Exists(ctx, "ctxuser", ""); return err },
		"VerifyPassword":          func() error { _, err := s.VerifyPassword(ctx, "ctxuser", "pw-123456"); return err },
		"GetUserByUsername":       func() error { _, err := s.GetUserByUsername(ctx, "ctxuser"); return err },
		"GetUserByID":             func() error { _, err := s.GetUserByID(ctx, uid); return err },
		"ListUsers":               func() error { _, err := s.ListUsers(ctx); return err },
		"DeleteUser":              func() error { return s.DeleteUser(ctx, uid) },
		"CreateToken":             func() error { _, err := s.CreateToken(ctx, TokenInvite, 0, "", time.Hour); return err },
		"ConsumeToken":            func() error { _, err := s.ConsumeToken(ctx, tok, TokenResetPassword); return err },
		"AddCredential":           func() error { return s.AddCredential(ctx, uid, cred) },
		"Credentials":             func() error { _, err := s.Credentials(ctx, uid); return err },
		"UpdateCredential":        func() error { return s.UpdateCredential(ctx, cred) },
		"DeleteCredential":        func() error { return s.DeleteCredential(ctx, uid, "c") },
		"CreateRefreshToken":      func() error { _, err := s.CreateRefreshToken(ctx, uid, time.Hour, false, "", ""); return err },
		"ValidateRefreshToken":    func() error { _, _, err := s.ValidateRefreshToken(ctx, refresh); return err },
		"RotateRefreshToken":      func() error { _, _, _, err := s.RotateRefreshToken(ctx, refresh, time.Minute); return err },
		"ConsumeRefreshToken":     func() error { return s.ConsumeRefreshToken(ctx, refresh) },
		"ConsumeRefreshTokenOnce": func() error { _, err := s.ConsumeRefreshTokenOnce(ctx, refresh); return err },
		"CleanupExpired":          func() error { return s.CleanupExpired(ctx) },
		"ListSessions":            func() error { _, err := s.ListSessions(ctx, uid, ""); return err },
		"RevokeSession":           func() error { return s.RevokeSession(ctx, uid, "x") },
		"RevokeOtherSessions":     func() error { _, err := s.RevokeOtherSessions(ctx, uid, refresh); return err },
		"RevokeAllSessions":       func() error { return s.RevokeAllSessions(ctx, uid) },
		"SetTOTPSecret":           func() error { return s.SetTOTPSecret(ctx, uid, "S") },
		"GetTOTPSecret":           func() error { _, _, err := s.GetTOTPSecret(ctx, uid); return err },
		"EnableTOTP":              func() error { return s.EnableTOTP(ctx, uid) },
		"DisableTOTP":             func() error { return s.DisableTOTP(ctx, uid) },
		"GenerateBackupCodes":     func() error { _, err := s.GenerateBackupCodes(ctx, uid, 3); return err },
		"CreateDeviceKey":         func() error { _, _, err := s.CreateDeviceKey(ctx, uid, DeviceKeyMeta{DeviceID: "tv2"}); return err },
		"LookupDeviceKey":         func() error { _, err := s.LookupDeviceKey(ctx, dk); return err },
		"TouchDeviceKey":          func() error { return s.TouchDeviceKey(ctx, dkID, "ip") },
		"ListDeviceKeys":          func() error { _, err := s.ListDeviceKeys(ctx, uid); return err },
		"DeviceProfiles":          func() error { _, err := s.DeviceProfiles(ctx, "tv"); return err },
		"RevokeDeviceKey":         func() error { return s.RevokeDeviceKey(ctx, dkID, uid) },
		"RevokeAllDeviceKeys":     func() error { return s.RevokeAllDeviceKeys(ctx, uid) },
		"SetDevicePIN":            func() error { return s.SetDevicePIN(ctx, dkID, uid, "1234") },
	}
	for name, call := range calls {
		start := time.Now()
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want context.Canceled", name, err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s took %v on a cancelled ctx", name, d)
		}
	}

	// The remaining helpers swallow the error into their return value, so
	// assert the observable effect instead.
	if s.HasPasskey(ctx, uid) {
		t.Error("HasPasskey on a cancelled ctx should be false")
	}
	if n := s.CountBackupCodes(ctx, uid); n != 0 {
		t.Errorf("CountBackupCodes on a cancelled ctx = %d", n)
	}
	if err := s.CheckDevicePIN(ctx, withPIN(key), "1234", time.Now()); !errors.Is(err, context.Canceled) {
		t.Errorf("CheckDevicePIN err = %v, want context.Canceled", err)
	}

	// None of the cancelled writes landed.
	u, err := s.GetUserByID(live, uid)
	if err != nil || u.Status != StatusActive || u.Email != "" {
		t.Fatalf("user after cancelled calls = %+v, %v; want untouched", u, err)
	}
	if _, _, o, err := s.RotateRefreshToken(live, refresh, time.Minute); err != nil || o != RefreshRotated {
		t.Fatalf("refresh token after cancelled calls: outcome=%v err=%v; want still rotatable", o, err)
	}
}

func withPIN(k *DeviceKey) *DeviceKey {
	c := *k
	c.HasPIN = true
	c.pinHash = "x"
	return &c
}

// A cancelled consume must not burn the backup code: the user can still use it
// once the request is retried with a live context (and it is single-use).
func TestConsumeBackupCodeCancelledDoesNotBurnCode(t *testing.T) {
	s := newTestStore(t)
	live := context.Background()
	uid, _ := s.CreateUser(live, "mfa", "pw-123456", RoleUser)
	codes, err := s.GenerateBackupCodes(live, uid, 2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s.ConsumeBackupCode(ctx, uid, codes[0]) {
		t.Fatal("cancelled consume must not succeed")
	}
	if !s.ConsumeBackupCode(live, uid, codes[0]) {
		t.Fatal("code must still be redeemable after a cancelled attempt")
	}
	if s.ConsumeBackupCode(live, uid, codes[0]) {
		t.Fatal("code must be single-use")
	}
}
