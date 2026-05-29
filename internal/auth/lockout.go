package auth

import (
	"strings"
	"sync"
	"time"
)

// Lockout is an in-memory brute-force guard keyed by username. After
// MaxFailures consecutive failed login attempts the key is locked for
// LockDuration. A successful login (or the lock expiring) resets the counter.
//
// In-memory is deliberate: a single-instance self-hosted app doesn't need a
// shared store, and losing the state on restart only ever HELPS a legitimate
// user (a restart clears a lock) — it never weakens the guard against a live
// attacker, who can't trigger restarts.
type Lockout struct {
	mu          sync.Mutex
	attempts    map[string]*attemptState
	MaxFailures int
	LockWindow  time.Duration
}

type attemptState struct {
	failures    int
	lockedUntil time.Time
	lastSeen    time.Time
}

// NewLockout builds a limiter. maxFailures<=0 disables locking entirely.
func NewLockout(maxFailures int, lockWindow time.Duration) *Lockout {
	return &Lockout{attempts: map[string]*attemptState{}, MaxFailures: maxFailures, LockWindow: lockWindow}
}

func normalizeKey(k string) string { return strings.ToLower(strings.TrimSpace(k)) }

// Locked reports whether key is currently locked and, if so, how long remains.
func (l *Lockout) Locked(key string) (bool, time.Duration) {
	if l == nil || l.MaxFailures <= 0 {
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.attempts[normalizeKey(key)]
	if s == nil {
		return false, 0
	}
	if rem := time.Until(s.lockedUntil); rem > 0 {
		return true, rem
	}
	return false, 0
}

// Fail records a failed attempt, locking the key once it hits MaxFailures.
func (l *Lockout) Fail(key string) {
	if l == nil || l.MaxFailures <= 0 {
		return
	}
	k := normalizeKey(key)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gcLocked()
	s := l.attempts[k]
	if s == nil {
		s = &attemptState{}
		l.attempts[k] = s
	}
	s.failures++
	s.lastSeen = time.Now()
	if s.failures >= l.MaxFailures {
		s.lockedUntil = time.Now().Add(l.LockWindow)
		s.failures = 0 // restart the count after the lock window
	}
}

// Reset clears all failure state for a key (call on a successful login).
func (l *Lockout) Reset(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, normalizeKey(key))
}

// gcLocked drops stale entries (no activity for 1h). Caller holds the mutex.
func (l *Lockout) gcLocked() {
	cutoff := time.Now().Add(-time.Hour)
	for k, s := range l.attempts {
		if s.lastSeen.Before(cutoff) && time.Now().After(s.lockedUntil) {
			delete(l.attempts, k)
		}
	}
}
