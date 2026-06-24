package auth

import (
	"testing"
	"time"
)

func TestRotateRefreshToken_Rotated(t *testing.T) {
	s := newTestStore(t)
	uid, _ := s.CreateUser("bob", "pass", RoleUser)
	tok, _ := s.CreateRefreshToken(uid, time.Hour, false, "ua", "ip")

	u, _, outcome, err := s.RotateRefreshToken(tok, 30*time.Second)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if outcome != RefreshRotated {
		t.Fatalf("outcome = %d, want RefreshRotated", outcome)
	}
	if u == nil || u.ID != uid {
		t.Error("should return the owning user")
	}
}

func TestRotateRefreshToken_GraceReissueOnConcurrent(t *testing.T) {
	s := newTestStore(t)
	uid, _ := s.CreateUser("bob", "pass", RoleUser)
	tok, _ := s.CreateRefreshToken(uid, time.Hour, false, "ua", "ip")

	// First rotation consumes it.
	if _, _, o1, _ := s.RotateRefreshToken(tok, 30*time.Second); o1 != RefreshRotated {
		t.Fatalf("first = %d, want RefreshRotated", o1)
	}
	// A concurrent refresh re-presents the SAME token within the grace window →
	// reissue, NOT revoke. This is the deploy/multi-tab case.
	_, _, o2, err := s.RotateRefreshToken(tok, 30*time.Second)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if o2 != RefreshGraceReissue {
		t.Errorf("second = %d, want RefreshGraceReissue", o2)
	}
}

func TestRotateRefreshToken_ReuseAfterGrace(t *testing.T) {
	s := newTestStore(t)
	uid, _ := s.CreateUser("bob", "pass", RoleUser)
	tok, _ := s.CreateRefreshToken(uid, time.Hour, false, "ua", "ip")

	if _, _, o1, _ := s.RotateRefreshToken(tok, 30*time.Second); o1 != RefreshRotated {
		t.Fatalf("first = %d", o1)
	}
	// grace=0 → any prior consume is "after the window" → reuse (theft).
	_, _, o2, _ := s.RotateRefreshToken(tok, 0)
	if o2 != RefreshReuse {
		t.Errorf("replay past grace = %d, want RefreshReuse", o2)
	}
}

func TestRotateRefreshToken_Invalid(t *testing.T) {
	s := newTestStore(t)
	if _, _, o, _ := s.RotateRefreshToken("nope", 30*time.Second); o != RefreshInvalid {
		t.Errorf("unknown token = %d, want RefreshInvalid", o)
	}
	// Expired token → invalid.
	uid, _ := s.CreateUser("bob", "pass", RoleUser)
	expired, _ := s.CreateRefreshToken(uid, -time.Hour, false, "ua", "ip")
	if _, _, o, _ := s.RotateRefreshToken(expired, 30*time.Second); o != RefreshInvalid {
		t.Errorf("expired token = %d, want RefreshInvalid", o)
	}
}

func TestConsumedTokenHiddenFromSessions(t *testing.T) {
	s := newTestStore(t)
	uid, _ := s.CreateUser("bob", "pass", RoleUser)
	tok, _ := s.CreateRefreshToken(uid, time.Hour, false, "ua", "ip")

	sessions, _ := s.ListSessions(uid, "")
	if len(sessions) != 1 {
		t.Fatalf("active sessions = %d, want 1", len(sessions))
	}
	// Rotating consumes the token → it must drop out of the active-sessions list.
	s.RotateRefreshToken(tok, 30*time.Second)
	sessions, _ = s.ListSessions(uid, "")
	if len(sessions) != 0 {
		t.Errorf("after rotation active sessions = %d, want 0 (consumed hidden)", len(sessions))
	}
}
