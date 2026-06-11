package transcode

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// stubProbe replaces the duration probe and shortens the retry backoff for the
// duration of the test. probe receives the attempt ordinal (1 = the in-session
// startup probe, 2+ = background retries).
func stubProbe(t *testing.T, backoff time.Duration, probe func(call int32) float64) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	origFn, origBackoff := probeDurationFn, durationRetryBackoff
	probeDurationFn = func(ctx context.Context, ffmpegPath, inputURL string) float64 {
		return probe(calls.Add(1))
	}
	durationRetryBackoff = backoff
	t.Cleanup(func() { probeDurationFn, durationRetryBackoff = origFn, origBackoff })
	return &calls
}

// A session born EVENT because the startup probe failed must re-probe in
// background; once the duration lands in the manager cache, the NEXT session
// of the same raw key is born VOD without probing again.
func TestDurationRetryUnlocksVODOnNextSession(t *testing.T) {
	stubCaps(t)
	m := lifecycleManager(t)
	m.SetVODMode(VODAll)
	calls := stubProbe(t, 10*time.Millisecond, func(call int32) float64 {
		if call == 1 {
			return 0 // startup probe fails → EVENT
		}
		return 60 // first background retry succeeds
	})

	s1, err := m.GetOrStart(context.Background(), HLSStartOpts{
		Key: "raw", Source: &fakeSource{}, SourceSize: 1, NativeHLS: true,
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	if s1.IsVOD() {
		t.Fatal("session with unknown duration must be born EVENT")
	}

	deadline := time.Now().Add(3 * time.Second)
	for m.cachedDuration("raw") == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.cachedDuration("raw"); got != 60 {
		t.Fatalf("cachedDuration=%v want 60 (background retry should populate it)", got)
	}
	// The LIVE session must stay EVENT: switching EXT-X-PLAYLIST-TYPE
	// mid-session violates the HLS spec — only the next session upgrades.
	if s1.IsVOD() {
		t.Fatal("live session must remain EVENT even after the re-probe")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("probe calls=%d want 2 (startup + one successful retry)", got)
	}

	m.Close(s1.Key)
	s2, err := m.GetOrStart(context.Background(), HLSStartOpts{
		Key: "raw", Source: &fakeSource{}, SourceSize: 1, NativeHLS: true,
	})
	if err != nil {
		t.Fatalf("GetOrStart (respawn): %v", err)
	}
	defer m.Close(s2.Key)
	if !s2.IsVOD() {
		t.Fatal("respawned session must be born VOD from the cached duration")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("probe calls=%d want 2 (respawn must use the cache, not re-probe)", got)
	}
}

// stop() must cancel the pending background re-probe — no goroutine outliving
// the session, no probe against a torn-down loopback server.
func TestStopCancelsDurationRetry(t *testing.T) {
	stubCaps(t)
	m := lifecycleManager(t)
	m.SetVODMode(VODAll) // retry only fires when VOD is enabled (mode != off)
	calls := stubProbe(t, 150*time.Millisecond, func(int32) float64 { return 0 })

	s, err := m.GetOrStart(context.Background(), HLSStartOpts{
		Key: "raw2", Source: &fakeSource{}, SourceSize: 1,
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe calls=%d want 1 (startup only) before the backoff elapses", got)
	}

	m.Close(s.Key) // stop() fires retryCancel before the 150ms backoff elapses

	time.Sleep(400 * time.Millisecond) // > backoff: a leaked retry would have fired
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe calls=%d want 1 — stop() must cancel the pending retry", got)
	}
}

// All retries failing must give up after durationRetryAttempts (bounded work,
// no infinite re-probe loop against a dead swarm).
func TestDurationRetryGivesUpAfterMaxAttempts(t *testing.T) {
	stubCaps(t)
	m := lifecycleManager(t)
	m.SetVODMode(VODAll) // retry only fires when VOD is enabled (mode != off)
	calls := stubProbe(t, 10*time.Millisecond, func(int32) float64 { return 0 })

	s, err := m.GetOrStart(context.Background(), HLSStartOpts{
		Key: "raw3", Source: &fakeSource{}, SourceSize: 1,
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	defer m.Close(s.Key)

	deadline := time.Now().Add(2 * time.Second)
	want := int32(1 + durationRetryAttempts) // startup + bounded retries
	for calls.Load() < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // would catch an extra attempt
	if got := calls.Load(); got != want {
		t.Fatalf("probe calls=%d want %d (startup + %d retries, then give up)", got, want, durationRetryAttempts)
	}
	if got := m.cachedDuration("raw3"); got != 0 {
		t.Fatalf("cachedDuration=%v want 0 (all probes failed)", got)
	}
}

// With VOD disabled entirely a session can never become VOD, so the background
// re-probe must not fire — only the startup probe runs.
func TestDurationRetrySkippedWhenVODOff(t *testing.T) {
	stubCaps(t)
	m := lifecycleManager(t)
	calls := stubProbe(t, 10*time.Millisecond, func(int32) float64 { return 0 })

	s, err := m.GetOrStart(context.Background(), HLSStartOpts{
		Key: "raw4", Source: &fakeSource{}, SourceSize: 1,
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	defer m.Close(s.Key)

	time.Sleep(150 * time.Millisecond) // would catch a retry attempt
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe calls=%d want 1 (startup only — no retry with VOD off)", got)
	}
}
