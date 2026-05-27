package ai

import (
	"sync"
	"time"
)

// Circuit-breaker thresholds, mirroring SelfAgent's defaults: trip after a small
// number of consecutive failures, then skip the model for a cooldown so the
// chain doesn't keep paying the timeout on a dead provider. Rate limits get a
// shorter, separate backoff (the provider is up, just throttling us).
const (
	breakerFailureThreshold = 2
	breakerOpenDuration     = 10 * time.Minute
	breakerRateLimitBackoff = 3 * time.Minute
)

// breaker is an in-memory per-slot circuit breaker. State lives for the process
// lifetime only — that's enough for title identification, which is infrequent
// (once per play) and tolerant of a cold start re-probing a provider.
type breaker struct {
	mu    sync.Mutex
	state map[string]*slotState
}

type slotState struct {
	failures  int
	openUntil time.Time
}

func newBreaker() *breaker {
	return &breaker{state: map[string]*slotState{}}
}

// available reports whether a slot may be tried right now (closed/half-open).
func (b *breaker) available(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.state[id]
	if s == nil {
		return true
	}
	return time.Now().After(s.openUntil)
}

func (b *breaker) recordSuccess(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.state, id)
}

func (b *breaker) recordFailure(id string, rateLimited bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.state[id]
	if s == nil {
		s = &slotState{}
		b.state[id] = s
	}
	s.failures++
	switch {
	case rateLimited:
		s.openUntil = time.Now().Add(breakerRateLimitBackoff)
	case s.failures >= breakerFailureThreshold:
		s.openUntil = time.Now().Add(breakerOpenDuration)
	}
}
