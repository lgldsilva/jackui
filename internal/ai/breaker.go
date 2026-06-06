package ai

import (
	"sync"
	"time"
)

// Circuit-breaker thresholds. Two distinct faults are tracked separately because
// they have different blast radius:
//
//   - A MODEL fault (bad output, timeouts) is specific to one slot → trip just
//     that slot after a couple of consecutive failures.
//   - A RATE LIMIT (429) is almost always the PROVIDER's free quota, shared across
//     ALL its models → parking one slot is useless (the next model on the same key
//     429s too). So a 429 parks the WHOLE PROVIDER, for the vendor's Retry-After
//     window when known (a per-minute reset is seconds; a per-DAY quota can be
//     hours), capped so a bogus huge hint can't wedge a provider forever.
const (
	breakerFailureThreshold = 2
	breakerOpenDuration     = 10 * time.Minute
	breakerRateLimitBackoff = 3 * time.Minute // fallback when the 429 carried no Retry-After
	breakerRateLimitMaxWait = 24 * time.Hour  // cap (covers a daily free quota)
)

// breaker is an in-memory circuit breaker. State lives for the process lifetime
// only — enough for title identification, which is infrequent (once per play) and
// tolerant of a cold start re-probing a provider.
type breaker struct {
	mu       sync.Mutex
	slot     map[string]*slotState // per-slot model faults
	provider map[string]time.Time  // per-provider rate-limit park (openUntil)
}

type slotState struct {
	failures  int
	openUntil time.Time
}

func newBreaker() *breaker {
	return &breaker{slot: map[string]*slotState{}, provider: map[string]time.Time{}}
}

// available reports whether a slot may be tried right now. It's blocked if EITHER
// its provider is rate-limit-parked OR the slot itself is open from model faults.
func (b *breaker) available(provider, id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if until, ok := b.provider[provider]; ok && time.Now().Before(until) {
		return false
	}
	s := b.slot[id]
	return s == nil || time.Now().After(s.openUntil)
}

// recordRateLimit parks the WHOLE provider (its free quota is shared across
// models) for the vendor's Retry-After, capped; a missing hint falls back to a
// short default. This is the "provider hit its limit" case, not a model fault.
func (b *breaker) recordRateLimit(provider string, retryAfter time.Duration) {
	wait := retryAfter
	if wait <= 0 {
		wait = breakerRateLimitBackoff
	}
	if wait > breakerRateLimitMaxWait {
		wait = breakerRateLimitMaxWait
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.provider[provider] = time.Now().Add(wait)
}

// recordFailure is a MODEL-specific fault: trip the slot after a couple of
// consecutive failures so the chain stops paying its timeout.
func (b *breaker) recordFailure(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.slot[id]
	if s == nil {
		s = &slotState{}
		b.slot[id] = s
	}
	s.failures++
	if s.failures >= breakerFailureThreshold {
		s.openUntil = time.Now().Add(breakerOpenDuration)
	}
}

// recordSuccess clears the slot's faults AND its provider's rate-limit park — a
// model answering proves the key isn't actually exhausted.
func (b *breaker) recordSuccess(provider, id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.slot, id)
	delete(b.provider, provider)
}
