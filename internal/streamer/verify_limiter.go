package streamer

import (
	"context"
	"errors"
	"log"
	"sync"
)

// ErrVerifyLimiterClosed is returned by AcquireContext after Shutdown.
var ErrVerifyLimiterClosed = errors.New("verify limiter closed")

// verifyLimiter caps how many piece-hash jobs may run at once. Independent of
// the download scheduler's max_active: downloads can run in parallel while
// disk-bound rechecks stay serial (or lightly parallel on SSD).
//
// Zero value is unusable — construct with newVerifyLimiter.
type verifyLimiter struct {
	mu     sync.Mutex
	ch     chan struct{} // closed to broadcast wakeups to all waiters
	limit  int
	active int
	closed bool
}

func newVerifyLimiter(limit int) *verifyLimiter {
	return &verifyLimiter{
		limit: normalizeVerifyLimit(limit),
		ch:    make(chan struct{}),
	}
}

func normalizeVerifyLimit(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// broadcastLocked wakes every waiter snapshotted on the current channel.
// Caller must hold l.mu.
func (l *verifyLimiter) broadcastLocked() {
	close(l.ch)
	l.ch = make(chan struct{})
}

// Acquire blocks until a verify slot is free, then takes it.
func (l *verifyLimiter) Acquire() {
	if l == nil {
		return
	}
	if err := l.AcquireContext(context.Background()); err != nil {
		log.Printf("streamer: verifyLimiter.Acquire: %v", err)
	}
}

// AcquireContext blocks until a verify slot is free or ctx is canceled, or the
// limiter is shut down. Returns ErrVerifyLimiterClosed after Shutdown.
func (l *verifyLimiter) AcquireContext(ctx context.Context) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return ErrVerifyLimiterClosed
		}
		if l.active < l.limit {
			l.active++
			l.mu.Unlock()
			return nil
		}
		wait := l.ch
		l.mu.Unlock()

		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Shutdown unblocks all waiters; subsequent AcquireContext returns ErrVerifyLimiterClosed.
func (l *verifyLimiter) Shutdown() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	l.broadcastLocked()
}

// Release frees a verify slot and wakes a waiter.
func (l *verifyLimiter) Release() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active > 0 {
		l.active--
	} else {
		log.Printf("streamer: verifyLimiter.Release() called when active == 0 (orphan call)")
	}
	l.broadcastLocked()
}

// SetLimit updates the concurrency cap live (>= 1). Waiters are woken so they
// re-check against the new limit. In-flight acquires may temporarily leave
// active > limit until they Release. No-op after Shutdown.
func (l *verifyLimiter) SetLimit(n int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.limit = normalizeVerifyLimit(n)
	l.broadcastLocked()
}

// Limit returns the current cap (for diagnostics / tests).
func (l *verifyLimiter) Limit() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit
}
