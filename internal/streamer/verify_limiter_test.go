package streamer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVerifyLimiter_SerializesWhenLimitOne(t *testing.T) {
	l := newVerifyLimiter(1)
	var concurrent atomic.Int32
	var maxSeen atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Acquire()
			n := concurrent.Add(1)
			for {
				cur := maxSeen.Load()
				if n <= cur || maxSeen.CompareAndSwap(cur, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			concurrent.Add(-1)
			l.Release()
		}()
	}
	wg.Wait()
	if maxSeen.Load() != 1 {
		t.Fatalf("max concurrent = %d, want 1", maxSeen.Load())
	}
}

func TestVerifyLimiter_AllowsParallelUpToLimit(t *testing.T) {
	l := newVerifyLimiter(2)
	var concurrent atomic.Int32
	var maxSeen atomic.Int32
	entered := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Acquire()
			n := concurrent.Add(1)
			for {
				cur := maxSeen.Load()
				if n <= cur || maxSeen.CompareAndSwap(cur, n) {
					break
				}
			}
			entered <- struct{}{}
			// Hold until both have entered so max concurrent is observable.
			time.Sleep(50 * time.Millisecond)
			concurrent.Add(-1)
			l.Release()
		}()
	}
	// Wait for both acquisitions without hanging the test forever.
	deadline := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-deadline:
			t.Fatal("timed out waiting for both acquires")
		}
	}
	wg.Wait()
	if maxSeen.Load() != 2 {
		t.Fatalf("max concurrent = %d, want 2", maxSeen.Load())
	}
}

func TestVerifyLimiter_SetLimitWakesWaiters(t *testing.T) {
	l := newVerifyLimiter(1)
	l.Acquire() // hold the only slot
	done := make(chan struct{})
	go func() {
		l.Acquire()
		close(done)
		l.Release()
	}()
	// Waiter must be blocked while limit is 1 and slot is held.
	select {
	case <-done:
		t.Fatal("second acquire must block while limit=1 and slot held")
	case <-time.After(50 * time.Millisecond):
	}
	l.SetLimit(2) // free a second slot without releasing the first
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SetLimit(2) should allow the waiter to enter")
	}
	l.Release()
}

func TestNormalizeVerifyLimit(t *testing.T) {
	if got := normalizeVerifyLimit(0); got != 1 {
		t.Fatalf("0 → %d, want 1", got)
	}
	if got := normalizeVerifyLimit(-3); got != 1 {
		t.Fatalf("-3 → %d, want 1", got)
	}
	if got := normalizeVerifyLimit(4); got != 4 {
		t.Fatalf("4 → %d, want 4", got)
	}
}

func TestVerifyLimiter_AcquireContextCanceled(t *testing.T) {
	l := newVerifyLimiter(1)
	l.Acquire()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.AcquireContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireContext: got %v, want context.Canceled", err)
	}
	l.Release()
}

func TestVerifyLimiter_ShutdownUnblocksWaiter(t *testing.T) {
	l := newVerifyLimiter(1)
	l.Acquire()
	done := make(chan error, 1)
	go func() {
		done <- l.AcquireContext(context.Background())
	}()
	select {
	case <-done:
		t.Fatal("waiter should block until Shutdown")
	case <-time.After(50 * time.Millisecond):
	}
	l.Shutdown()
	select {
	case err := <-done:
		if !errors.Is(err, ErrVerifyLimiterClosed) {
			t.Fatalf("waiter err = %v, want ErrVerifyLimiterClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not unblock waiter")
	}
	l.Release()
}

func TestVerifyLimiter_CancelWhileWaiting(t *testing.T) {
	l := newVerifyLimiter(1)
	l.Acquire()

	for i := 0; i < 200; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- l.AcquireContext(ctx)
		}()

		// Stagger cancel timing so the waiter is usually blocked on the channel.
		time.Sleep(time.Duration(i%10) * time.Microsecond)
		cancel()

		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("iteration %d: got %v, want context.Canceled", i, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: AcquireContext did not return after cancel", i)
		}
	}
	l.Release()
}
