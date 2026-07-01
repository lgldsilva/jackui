package ai

import (
	"context"
	"testing"
	"time"
)

// TestProviderLimiterPaces: two calls to the same throttled provider are spaced ~interval
// apart; an unthrottled provider (and a nil limiter) return immediately.
func TestProviderLimiterPaces(t *testing.T) {
	// 600 rpm → 100ms interval (keeps the test fast but measurable).
	l := newProviderLimiter(map[string]int{"google": 600})
	ctx := context.Background()

	start := time.Now()
	_ = l.reserve(ctx, "google") // first: immediate
	first := time.Since(start)
	if first > 40*time.Millisecond {
		t.Fatalf("first reserve should be ~immediate, took %v", first)
	}
	_ = l.reserve(ctx, "google") // second: waits out the interval
	second := time.Since(start)
	if second < 90*time.Millisecond {
		t.Fatalf("second reserve should wait ~100ms, total was %v", second)
	}

	// A provider with no configured cap is not throttled.
	t0 := time.Now()
	_ = l.reserve(ctx, "groq")
	_ = l.reserve(ctx, "groq")
	if d := time.Since(t0); d > 40*time.Millisecond {
		t.Fatalf("unthrottled provider should not wait, took %v", d)
	}
}

// TestProviderLimiterNilAndEmpty: a nil limiter and an all-zero-rpm map are safe no-ops.
func TestProviderLimiterNilAndEmpty(t *testing.T) {
	var nilLimiter *providerLimiter
	if err := nilLimiter.reserve(context.Background(), "google"); err != nil {
		t.Fatalf("nil limiter reserve should be a no-op, got %v", err)
	}
	if l := newProviderLimiter(map[string]int{"google": 0}); l != nil {
		t.Fatal("a map with only zero rpm should produce a nil limiter")
	}
}

// TestProviderLimiterCtxCancel: reserve returns the ctx error instead of sleeping when the
// context is already done.
func TestProviderLimiterCtxCancel(t *testing.T) {
	l := newProviderLimiter(map[string]int{"google": 1}) // 60s interval
	_ = l.reserve(context.Background(), "google")        // claim the first slot
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.reserve(ctx, "google"); err == nil {
		t.Fatal("reserve should return ctx error when the wait is cancelled")
	}
}
