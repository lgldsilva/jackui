package auth

import (
	"testing"
	"time"
)

func TestLockoutLocksAfterThreshold(t *testing.T) {
	l := NewLockout(3, time.Minute)
	if locked, _ := l.Locked("alice"); locked {
		t.Fatal("fresh key should not be locked")
	}
	l.Fail("alice")
	l.Fail("alice")
	if locked, _ := l.Locked("alice"); locked {
		t.Fatal("should not lock before reaching the threshold")
	}
	l.Fail("alice") // 3rd → locks
	locked, rem := l.Locked("alice")
	if !locked || rem <= 0 {
		t.Fatalf("expected lock after threshold, got locked=%v rem=%v", locked, rem)
	}
}

func TestLockoutResetClears(t *testing.T) {
	l := NewLockout(2, time.Minute)
	l.Fail("bob")
	l.Reset("bob")
	l.Fail("bob") // back to 1, not 2 → still open
	if locked, _ := l.Locked("bob"); locked {
		t.Fatal("Reset should have cleared the prior failure")
	}
}

func TestLockoutCaseInsensitiveKey(t *testing.T) {
	l := NewLockout(2, time.Minute)
	l.Fail("Carol")
	l.Fail("carol") // same key normalized → 2 → locks
	if locked, _ := l.Locked("CAROL"); !locked {
		t.Fatal("keys should be normalized case-insensitively")
	}
}

func TestLockoutDisabledWhenMaxZero(t *testing.T) {
	l := NewLockout(0, time.Minute)
	for i := 0; i < 100; i++ {
		l.Fail("dave")
	}
	if locked, _ := l.Locked("dave"); locked {
		t.Fatal("maxFailures<=0 must disable locking")
	}
}
