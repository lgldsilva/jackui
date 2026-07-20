package auth

import (
	"testing"
	"time"
)

func TestIPRateLimiterAllowsWithinWindow(t *testing.T) {
	l := NewIPRateLimiter(5, time.Minute)
	for i := 0; i < 5; i++ {
		allowed, _ := l.Allow("127.0.0.1")
		if !allowed {
			t.Fatalf("request %d should be allowed within limit of 5", i+1)
		}
	}
}

func TestIPRateLimiterBlocksAfterThreshold(t *testing.T) {
	l := NewIPRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if allowed, _ := l.Allow("127.0.0.1"); !allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	// 4th request exceeds limit.
	allowed, retry := l.Allow("127.0.0.1")
	if allowed {
		t.Fatal("4th request should be blocked")
	}
	if retry <= 0 {
		t.Fatalf("retryAfter should be positive, got %v", retry)
	}
}

func TestIPRateLimiterDifferentIPsIndependent(t *testing.T) {
	l := NewIPRateLimiter(2, time.Minute)
	l.Allow("127.0.0.1")
	l.Allow("127.0.0.1") // 2/2 for 127.0.0.1

	// Different IP should not be affected.
	allowed, _ := l.Allow("10.0.0.1")
	if !allowed {
		t.Fatal("a different IP should not be rate-limited")
	}
	// 127.0.0.1 should be blocked now.
	if allowed, _ := l.Allow("127.0.0.1"); allowed {
		t.Fatal("127.0.0.1 should be blocked after 2 requests")
	}
}

func TestIPRateLimiterDisabledWhenMaxZero(t *testing.T) {
	l := NewIPRateLimiter(0, time.Minute)
	for i := 0; i < 100; i++ {
		if allowed, _ := l.Allow("127.0.0.1"); !allowed {
			t.Fatal("maxRequests <= 0 must disable limiting")
		}
	}
}

func TestIPRateLimiterDisabledWhenNil(t *testing.T) {
	var l *IPRateLimiter = nil
	for i := 0; i < 10; i++ {
		if allowed, _ := l.Allow("127.0.0.1"); !allowed {
			t.Fatal("nil receiver must disable limiting")
		}
	}
}

func TestIPRateLimiterWindowResets(t *testing.T) {
	l := NewIPRateLimiter(2, 50*time.Millisecond)

	l.Allow("127.0.0.1")
	l.Allow("127.0.0.1")
	// Should be blocked now.
	if allowed, _ := l.Allow("127.0.0.1"); allowed {
		t.Fatal("should be blocked after 2 requests within window")
	}

	// Wait for window to expire.
	time.Sleep(60 * time.Millisecond)

	// Window expired → should be allowed again.
	allowed, _ := l.Allow("127.0.0.1")
	if !allowed {
		t.Fatal("request should be allowed after window expiry")
	}
}

func TestIPRateLimiterNilReceiver(t *testing.T) {
	var l *IPRateLimiter
	allowed, retry := l.Allow("127.0.0.1")
	if !allowed {
		t.Fatal("nil receiver should allow everything")
	}
	if retry != 0 {
		t.Fatalf("nil receiver should have zero retryAfter, got %v", retry)
	}
}
