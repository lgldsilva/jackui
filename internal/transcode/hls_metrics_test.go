package transcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/metrics/events"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func counterAt(t *testing.T, labels ...string) func() float64 {
	t.Helper()
	return func() float64 { return testutil.ToFloat64(events.HLSSessionFailures.WithLabelValues(labels...)) }
}

func startedTotal() float64 {
	var sum float64
	for _, mode := range []string{"vod", "event"} {
		for _, dec := range []string{"hw", "sw"} {
			sum += testutil.ToFloat64(events.HLSSessionsStarted.WithLabelValues(mode, dec))
		}
	}
	return sum
}

func admission(ev string) float64 {
	return testutil.ToFloat64(events.HLSAdmission.WithLabelValues(ev))
}

func probeFailures(probe, reason string) float64 {
	return testutil.ToFloat64(events.FFprobeFailures.WithLabelValues(probe, reason))
}

// A refused session (global cap → 429) and an owner-LRU eviction must each be
// counted — before, the 429s were invisible outside the access log.
func TestHLSMetrics_AdmissionEvents(t *testing.T) {
	m := lifecycleManager(t)
	m.SetSessionLimits(1, 0)
	seedSessions(m, "u1", 1, time.Now())
	before := admission(events.HLSAdmitRejectedCap)
	_, err := m.GetOrStart(context.Background(), HLSStartOpts{Key: "new", Source: &fakeSource{}, Owner: "u2"})
	if !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("err = %v, want ErrTooManySessions", err)
	}
	if got := admission(events.HLSAdmitRejectedCap) - before; got != 1 {
		t.Errorf("rejected_cap delta = %v, want 1", got)
	}

	m2 := lifecycleManager(t)
	m2.SetSessionLimits(0, 1)
	seedSessions(m2, "u1", 1, time.Now().Add(-time.Minute))
	beforeEvict := admission(events.HLSAdmitEvictedLRU)
	m2.mu.Lock()
	victim, aerr := m2.admitLocked("u1")
	m2.mu.Unlock()
	if aerr != nil || victim == nil {
		t.Fatalf("admit = (%v, %v), want an evicted victim", victim, aerr)
	}
	if got := admission(events.HLSAdmitEvictedLRU) - beforeEvict; got != 1 {
		t.Errorf("evicted_owner_lru delta = %v, want 1", got)
	}
}

// A successful build counts one start; a build that fails counts start_error.
func TestHLSMetrics_StartAndStartError(t *testing.T) {
	stubCaps(t)
	m := lifecycleManager(t)
	t.Cleanup(m.Stop)
	before := startedTotal()
	if _, err := m.GetOrStart(context.Background(), HLSStartOpts{
		Key: "ok", Source: &fakeSource{}, SourceSize: 1, KnownDurationSec: 10,
	}); err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	if got := startedTotal() - before; got != 1 {
		t.Errorf("sessions_started delta = %v, want 1", got)
	}

	ResetCachedForTesting() // no caps → buildSession fails
	failed := counterAt(t, events.HLSFailStartError)
	beforeFail := failed()
	if _, err := m.GetOrStart(context.Background(), HLSStartOpts{Key: "bad", Source: &fakeSource{}}); err == nil {
		t.Fatal("GetOrStart without caps must fail")
	}
	if got := failed() - beforeFail; got != 1 {
		t.Errorf("start_error delta = %v, want 1", got)
	}
}

// An encoder that exits non-zero on its own (not cancelled) is an ffmpeg_exit.
func TestHLSMetrics_FFmpegExitCounted(t *testing.T) {
	dir := t.TempDir()
	s := &HLSSession{
		Key:  "exit",
		Dir:  dir,
		spec: &encodeSpec{dir: dir, inputURL: "http://127.0.0.1:1/source", encoder: "libx264", ffmpegPath: "false", vod: true},
	}
	failed := counterAt(t, events.HLSFailFFmpegExit)
	before := failed()
	if err := s.launch(0); err != nil {
		t.Fatalf("launch: %v", err)
	}
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	<-done
	// The exit watcher records after close(done); poll briefly for the Inc.
	deadline := time.Now().Add(2 * time.Second)
	for failed()-before < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := failed() - before; got != 1 {
		t.Errorf("ffmpeg_exit delta = %v, want 1", got)
	}
}

// writeFakeFFprobe installs an "ffprobe" script next to a fake ffmpeg path so
// probeDurationSeekable runs it instead of the real binary.
func writeFakeFFprobe(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "ffmpeg")
}

// The duration probe failing is what silently degrades a play to EVENT/live
// (no seekbar); each failure class must be counted.
func TestHLSMetrics_DurationProbeFailures(t *testing.T) {
	cases := []struct {
		name, ffmpeg, reason string
	}{
		{"exec error", filepath.Join(t.TempDir(), "missing", "ffmpeg"), events.ProbeReasonError},
		{"garbage output", writeFakeFFprobe(t, "echo not-json"), events.ProbeReasonParse},
		{"no duration", writeFakeFFprobe(t, `echo '{"format":{}}'`), events.ProbeReasonNoDuration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := probeFailures(events.ProbeDuration, tc.reason)
			if d := probeDurationSeekable(context.Background(), tc.ffmpeg, "http://127.0.0.1:1/x"); d != 0 {
				t.Fatalf("duration = %v, want 0", d)
			}
			if got := probeFailures(events.ProbeDuration, tc.reason) - before; got != 1 {
				t.Errorf("%s delta = %v, want 1", tc.reason, got)
			}
		})
	}
	ok := writeFakeFFprobe(t, `echo '{"format":{"duration":"12.5"}}'`)
	before := probeFailures(events.ProbeDuration, events.ProbeReasonError) +
		probeFailures(events.ProbeDuration, events.ProbeReasonParse) +
		probeFailures(events.ProbeDuration, events.ProbeReasonNoDuration)
	if d := probeDurationSeekable(context.Background(), ok, "http://127.0.0.1:1/x"); d != 12.5 {
		t.Fatalf("duration = %v, want 12.5", d)
	}
	after := probeFailures(events.ProbeDuration, events.ProbeReasonError) +
		probeFailures(events.ProbeDuration, events.ProbeReasonParse) +
		probeFailures(events.ProbeDuration, events.ProbeReasonNoDuration)
	if after != before {
		t.Error("a successful probe must not count as a failure")
	}
}
