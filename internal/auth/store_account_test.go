package auth

import (
	"testing"
	"time"
)

func TestUpdateEmail_ResetsVerified(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateUserFull("alice", "alice@old.com", "secret123", RoleUser, StatusActive)
	if err != nil {
		t.Fatalf("CreateUserFull: %v", err)
	}
	if err := s.SetEmailVerified(id, ""); err != nil {
		t.Fatalf("SetEmailVerified: %v", err)
	}

	if err := s.UpdateEmail(id, "alice@new.com"); err != nil {
		t.Fatalf("UpdateEmail: %v", err)
	}

	u, err := s.GetUserByID(id)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if u.Email != "alice@new.com" {
		t.Errorf("Email = %q, want alice@new.com", u.Email)
	}
	if u.EmailVerified {
		t.Error("EmailVerified should reset to false after UpdateEmail")
	}
}

func TestEmailInUse(t *testing.T) {
	s := newTestStore(t)
	aliceID, _ := s.CreateUserFull("alice", "alice@test.com", "secret123", RoleUser, StatusActive)
	s.CreateUserFull("bob", "bob@test.com", "secret123", RoleUser, StatusActive)

	// Another user's address is in use.
	used, err := s.EmailInUse("bob@test.com", aliceID)
	if err != nil {
		t.Fatalf("EmailInUse: %v", err)
	}
	if !used {
		t.Error("bob@test.com should be in use for alice")
	}

	// One's OWN address is excluded.
	used, err = s.EmailInUse("alice@test.com", aliceID)
	if err != nil {
		t.Fatalf("EmailInUse: %v", err)
	}
	if used {
		t.Error("alice's own email must not count as in use")
	}

	// Free address.
	if used, _ = s.EmailInUse("free@test.com", aliceID); used {
		t.Error("free@test.com should not be in use")
	}

	// Empty never matches (rows with empty email would collide otherwise).
	if used, _ = s.EmailInUse("", aliceID); used {
		t.Error("empty email must never be in use")
	}
}

func TestSessions_PersistUserAgentAndIP(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateUserFull("alice", "alice@test.com", "secret123", RoleUser, StatusActive)

	tok, err := s.CreateRefreshToken(id, time.Hour, false, "Mozilla/5.0 (iPhone)", "10.0.0.7")
	if err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}

	sessions, err := s.ListSessions(id, tok)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	si := sessions[0]
	if si.UserAgent != "Mozilla/5.0 (iPhone)" {
		t.Errorf("UserAgent = %q", si.UserAgent)
	}
	if si.IP != "10.0.0.7" {
		t.Errorf("IP = %q", si.IP)
	}
	if !si.Current {
		t.Error("session should be flagged current")
	}
}
