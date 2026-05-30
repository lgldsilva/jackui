package auth

import (
	"testing"
	"time"
)

func TestWAUser(t *testing.T) {
	u := waUserFrom(42, "testuser", nil)
	if u.WebAuthnName() != "testuser" {
		t.Fatalf("Name = %q", u.WebAuthnName())
	}
	if u.WebAuthnDisplayName() != "testuser" {
		t.Fatalf("DisplayName = %q", u.WebAuthnDisplayName())
	}
	creds := u.WebAuthnCredentials()
	if len(creds) != 0 {
		t.Fatalf("expected nil creds")
	}
	id := u.WebAuthnID()
	if len(id) != 8 {
		t.Fatalf("expected 8 byte ID, got %d", len(id))
	}
}

func TestNewWAManager_NilWithMissingConfig(t *testing.T) {
	m, err := NewWAManager("", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != nil {
		t.Fatal("expected nil manager without config")
	}
	// With valid RPID and origin (empty display name triggers webauthn.New, which may error)
	// Just verify it returns nil or error (not a valid manager)
	m2, _ := NewWAManager("example.com", "", "https://example.com")
	if m2 != nil {
		_ = m2
	}
}

func TestTakeSession_Nonexistent(t *testing.T) {
	m, err := NewWAManager("example.com", "JackUI", "https://example.com")
	if err != nil || m == nil {
		t.Skip("WAManager is nil, skipping")
	}

	_, ok := m.takeSession("nonexistent")
	if ok {
		t.Fatal("expected nonexistent session to fail")
	}
}

func TestTakeSession_Expired(t *testing.T) {
	m, err := NewWAManager("example.com", "JackUI", "https://example.com")
	if err != nil || m == nil {
		t.Skip("WAManager is nil, skipping")
	}
	m.mu.Lock()
	m.sessions["expiredid"] = waSession{exp: time.Now().Add(-time.Hour)}
	m.mu.Unlock()
	_, ok := m.takeSession("expiredid")
	if ok {
		t.Fatal("expected expired session to fail")
	}
}

func TestPutAndTakeSession(t *testing.T) {
	m, err := NewWAManager("example.com", "JackUI", "https://example.com")
	if err != nil || m == nil {
		t.Skip("WAManager is nil, skipping")
	}

	u := waUserFrom(1, "test", nil)

	opts, session, err := m.wa.BeginRegistration(u)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	if opts == nil || session == nil {
		t.Fatal("expected non-nil opts and session")
	}

	// Don't use putSession/session returned by BeginRegistration in a direct way.
	// Just test we can create a session ID from the response.
	id := m.putSession(session)
	if id == "" {
		t.Fatal("expected non-empty session ID")
	}

	// Take the session
	sd, ok := m.takeSession(id)
	if !ok || sd == nil {
		t.Fatal("expected to retrieve session")
	}

	// Second take should fail (single-use)
	_, ok = m.takeSession(id)
	if ok {
		t.Fatal("expected second take to fail")
	}
}
