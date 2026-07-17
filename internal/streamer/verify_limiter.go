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
	cond   *sync.Cond
	limit  int
	active int
	closed bool
}

func newVerifyLimiter(limit int) *verifyLimiter {
	l := &verifyLimiter{limit: normalizeVerifyLimit(limit)}
	l.cond = sync.NewCond(&l.mu)
	return l
}

func normalizeVerifyLimit(n int) int {
	if n < 1 {
		return 1
	}
	return n
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
	l.mu.Lock()
	defer l.mu.Unlock()
	for {
		if l.closed {
			return ErrVerifyLimiterClosed
		}
		if l.active < l.limit {
			l.active++
			return nil
		}
		if err := l.waitCond(ctx); err != nil {
			return err
		}
	}
}

func (l *verifyLimiter) waitCond(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	done := make(chan struct{})
	go func() {
		l.cond.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		l.cond.Broadcast()
		<-done
		return ctx.Err()
	}
}

// Shutdown unblocks all waiters; subsequent AcquireContext returns ErrVerifyLimiterClosed.
func (l *verifyLimiter) Shutdown() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.closed = true
	l.cond.Broadcast()
	l.mu.Unlock()
}

// Release frees a verify slot and wakes a waiter.
func (l *verifyLimiter) Release() {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.active > 0 {
		l.active--
	} else {
		log.Printf("streamer: verifyLimiter.Release() called when active == 0 (orphan call)")
	}
	l.cond.Signal()
	l.mu.Unlock()
}

// SetLimit updates the concurrency cap live (>= 1). Waiters are woken so they
// re-check against the new limit. In-flight acquires may temporarily leave
// active > limit until they Release. No-op after Shutdown.
func (l *verifyLimiter) SetLimit(n int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.limit = normalizeVerifyLimit(n)
	l.cond.Broadcast()
	l.mu.Unlock()
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
