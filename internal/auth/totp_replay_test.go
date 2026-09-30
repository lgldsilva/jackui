package auth

import (
	"testing"
	"time"
)

// The replay gate: same step and any older step are refused; a strictly newer
// step is accepted; users are independent.
func TestTOTPReplayGate(t *testing.T) {
	g := NewTOTPReplayCache()
	if !g.Allow(1, 100) {
		t.Fatal("first use of step 100 must be allowed")
	}
	if g.Allow(1, 100) {
		t.Fatal("same step twice is a replay")
	}
	if g.Allow(1, 99) {
		t.Fatal("older step than the last accepted one is a replay (±1 skew window)")
	}
	if !g.Allow(1, 101) {
		t.Fatal("next window (newer step) must be allowed")
	}
	if !g.Allow(2, 100) {
		t.Fatal("a different user must not inherit user 1's floor")
	}
}

// Store-level wiring: validation still succeeds for a fresh code, refuses the
// same code, and accepts the following window's code. Uses real TOTP secrets
// and codes end to end.
func TestValidateTOTPForUser_SingleUsePerStep(t *testing.T) {
	s := newTestStore(t)
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	now := uint64(time.Now().Unix() / int64(totpStep.Seconds()))
	code := totpAt(secret, now)

	if !s.ValidateTOTPForUser(7, secret, code) {
		t.Fatal("fresh code must validate")
	}
	if s.ValidateTOTPForUser(7, secret, code) {
		t.Fatal("same code must be refused (replay)")
	}
	if !s.ValidateTOTPForUser(7, secret, totpAt(secret, now+1)) {
		t.Fatal("next-window code must validate")
	}
	if s.ValidateTOTPForUser(7, secret, "nope") {
		t.Fatal("malformed code must fail")
	}
	// A zero-value Store (no New) has no replay memory — plain validation only.
	var zero Store
	for attempt := 1; attempt <= 2; attempt++ {
		if !zero.ValidateTOTPForUser(7, secret, code) {
			t.Fatalf("zero-value Store degrades to plain validation (attempt %d refused)", attempt)
		}
	}
}
