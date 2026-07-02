package ai

import (
	"context"
	"sync"
	"time"
)

// providerLimiter paces calls PER PROVIDER to stay under a free-tier requests/min cap, so
// a burst benchmark (many models of the same provider fanned out concurrently, each running
// the full case set) doesn't trip a 429 and leave models "incomplete". It schedules rather
// than blocks: reserve() hands each call the next allowed start time (spaced by the
// provider's interval, shared across all its slots) and the caller sleeps until then.
//
// The scheduling is serialized under mu, but the sleep happens OUTSIDE the lock, so calls to
// the SAME provider are paced while different providers proceed independently. A provider
// with no configured interval is a no-op (zero overhead — the fast path returns before the
// lock).
type providerLimiter struct {
	mu        sync.Mutex
	nextFree  map[string]time.Time
	intervals map[string]time.Duration // provider -> min gap between calls; absent/0 = unthrottled
}

// newProviderLimiter builds a limiter from per-provider requests/min caps (rpm). A cap of
// 0 (or absent) leaves that provider unthrottled. Returns nil when nothing is throttled, so
// reserve() is a cheap no-op.
func newProviderLimiter(rpm map[string]int) *providerLimiter {
	intervals := map[string]time.Duration{}
	for p, r := range rpm {
		if r > 0 {
			intervals[p] = time.Minute / time.Duration(r)
		}
	}
	if len(intervals) == 0 {
		return nil
	}
	return &providerLimiter{nextFree: map[string]time.Time{}, intervals: intervals}
}

// reserve blocks until this call to `provider` may start (respecting its interval) or ctx is
// done. No-op for an unthrottled provider (or a nil limiter). Each reserve claims the next
// slot, so N concurrent callers to the same provider are spaced interval apart in arrival
// order — never bursting past the cap.
func (l *providerLimiter) reserve(ctx context.Context, provider string) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	iv, ok := l.intervals[provider]
	if !ok || iv <= 0 {
		l.mu.Unlock()
		return nil
	}
	now := time.Now()
	start := l.nextFree[provider]
	if start.Before(now) {
		start = now
	}
	l.nextFree[provider] = start.Add(iv)
	l.mu.Unlock()

	wait := time.Until(start)
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
