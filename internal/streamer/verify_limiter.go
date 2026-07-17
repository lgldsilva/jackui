package streamer

import "sync"

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
	l.mu.Lock()
	for l.active >= l.limit {
		l.cond.Wait()
	}
	l.active++
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
	}
	l.cond.Signal()
	l.mu.Unlock()
}

// SetLimit updates the concurrency cap live (>= 1). Waiters are woken so they
// re-check against the new limit. In-flight acquires may temporarily leave
// active > limit until they Release.
func (l *verifyLimiter) SetLimit(n int) {
	if l == nil {
		return
	}
	l.mu.Lock()
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
