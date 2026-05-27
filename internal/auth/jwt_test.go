package auth

import (
	"testing"
	"time"
)

func TestSignAndParseAccess(t *testing.T) {
	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-aaa"), 1*time.Hour)
	u := &User{ID: 42, Username: "alice", Role: RoleAdmin}

	tok, exp, err := tm.SignAccess(u)
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}
	if tok == "" {
		t.Fatal("expected non-empty token")
	}
	if time.Until(exp) <= 0 || time.Until(exp) > 1*time.Hour {
		t.Errorf("expiration off: %v", exp)
	}

	claims, err := tm.ParseAccess(tok)
	if err != nil {
		t.Fatalf("ParseAccess: %v", err)
	}
	if claims.UserID != 42 || claims.Username != "alice" || claims.Role != RoleAdmin {
		t.Errorf("claims wrong: %+v", claims)
	}
}

func TestParseExpiredToken(t *testing.T) {
	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-bbb"), -1*time.Second)
	u := &User{ID: 1, Username: "a", Role: RoleUser}
	tok, _, _ := tm.SignAccess(u)
	if _, err := tm.ParseAccess(tok); err == nil {
		t.Error("expected error for expired token")
	}
}

func TestParseTamperedToken(t *testing.T) {
	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-ccc"), 1*time.Hour)
	u := &User{ID: 1, Username: "a", Role: RoleUser}
	tok, _, _ := tm.SignAccess(u)

	// Tamper: change a byte in the middle
	tampered := tok[:len(tok)-5] + "XXXXX"
	if _, err := tm.ParseAccess(tampered); err == nil {
		t.Error("expected error for tampered token")
	}
}

func TestParseDifferentSecret(t *testing.T) {
	tm1 := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-111"), 1*time.Hour)
	tm2 := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-222"), 1*time.Hour)
	u := &User{ID: 1, Username: "a", Role: RoleUser}
	tok, _, _ := tm1.SignAccess(u)
	if _, err := tm2.ParseAccess(tok); err == nil {
		t.Error("expected error when verifying with different secret")
	}
}
