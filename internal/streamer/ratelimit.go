package streamer

import "golang.org/x/time/rate"

// RateLimits exposes the configured global bandwidth caps in bytes/sec.
// A value of 0 means unlimited.
func (s *Streamer) RateLimits() (down, up int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return limiterBytes(s.dlLimiter), limiterBytes(s.upLimiter)
}

// SetRateLimits updates the global download/upload bandwidth caps in bytes/sec.
// 0 = unlimited. Takes effect immediately — anacrolix re-reads the limiter on
// every chunk transfer.
func (s *Streamer) SetRateLimits(down, up int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	applyLimiter(s.dlLimiter, down)
	applyLimiter(s.upLimiter, up)
	s.cfg.MaxDownloadRate = down
	s.cfg.MaxUploadRate = up
}

// rateFromBytes converts a bytes/sec setting into a rate.Limit suitable for
// the anacrolix limiter. 0 means unlimited (rate.Inf).
func rateFromBytes(bps int64) rate.Limit {
	if bps <= 0 {
		return rate.Inf
	}
	return rate.Limit(bps)
}

// rateBurst picks a burst (token bucket size) appropriate for the given limit.
// anacrolix's docstring asks for "bigger than the largest Read" — chunks are
// at most 16 KiB plus the internal buffer of ~4 KiB, so a 64 KiB burst is
// safe and lets short spikes through without stalling the scheduler.
func rateBurst(bps int64) int {
	if bps <= 0 {
		return 1 << 16 // any non-zero burst works when limit is Inf
	}
	burst := int(bps / 4) // ~250ms worth of bytes
	const minBurst = 64 * 1024
	if burst < minBurst {
		burst = minBurst
	}
	return burst
}

// applyLimiter updates an existing limiter in place — anacrolix reads it on
// every chunk so the change is visible immediately. Setting bps<=0 means
// unlimited (rate.Inf, large burst).
func applyLimiter(l *rate.Limiter, bps int64) {
	if l == nil {
		return
	}
	if bps <= 0 {
		l.SetLimit(rate.Inf)
		l.SetBurst(1 << 16)
		return
	}
	l.SetLimit(rate.Limit(bps))
	l.SetBurst(rateBurst(bps))
}

// limiterBytes converts a limiter's current limit back to bytes/sec. Returns
// 0 when the limit is rate.Inf (unlimited).
func limiterBytes(l *rate.Limiter) int64 {
	if l == nil {
		return 0
	}
	lim := l.Limit()
	if lim == rate.Inf {
		return 0
	}
	return int64(lim)
}
