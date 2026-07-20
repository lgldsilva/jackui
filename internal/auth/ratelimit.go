package auth

import (
	"sync"
	"time"
)

// IPRateLimiter is an in-memory per-IP request rate limiter. After MaxRequests
// within Window the IP is locked for the remainder of that window.
//
// In-memory is deliberate: same reasoning as Lockout — single-instance
// self-hosted app, and a restart only ever helps legitimate users by resetting
// the count. An attacker cannot trigger restarts.
type IPRateLimiter struct {
	mu          sync.Mutex
	entries     map[string]*ipRateState
	MaxRequests int
	Window      time.Duration
}

type ipRateState struct {
	count       int
	windowStart time.Time
	lockedUntil time.Time
	lastSeen    time.Time
}

// NewIPRateLimiter builds a rate limiter. maxRequests <= 0 disables limiting.
func NewIPRateLimiter(maxRequests int, window time.Duration) *IPRateLimiter {
	return &IPRateLimiter{
		entries:     make(map[string]*ipRateState),
		MaxRequests: maxRequests,
		Window:      window,
	}
}

// Allow checks whether ip may proceed. Returns (allowed, retryAfter).
// retryAfter is meaningful only when allowed is false.
func (l *IPRateLimiter) Allow(ip string) (bool, time.Duration) {
	if l == nil || l.MaxRequests <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gcLocked()

	s := l.entries[ip]
	now := time.Now()

	if s == nil {
		l.entries[ip] = &ipRateState{count: 1, windowStart: now, lastSeen: now}
		return true, 0
	}

	s.lastSeen = now

	// Check if currently locked.
	if rem := time.Until(s.lockedUntil); rem > 0 {
		return false, rem
	}

	// Window expired → reset.
	if now.Sub(s.windowStart) >= l.Window {
		s.count = 1
		s.windowStart = now
		return true, 0
	}

	// Within the current window.
	s.count++
	if s.count > l.MaxRequests {
		// Lock until the window expires.
		s.lockedUntil = s.windowStart.Add(l.Window)
		return false, s.lockedUntil.Sub(now)
	}

	return true, 0
}

// gcLocked drops entries that haven't been seen for at least 2 windows and are
// no longer locked. Caller holds the mutex.
func (l *IPRateLimiter) gcLocked() {
	cutoff := time.Now().Add(-2 * l.Window)
	for k, s := range l.entries {
		if s.lastSeen.Before(cutoff) && time.Now().After(s.lockedUntil) {
			delete(l.entries, k)
		}
	}
}
