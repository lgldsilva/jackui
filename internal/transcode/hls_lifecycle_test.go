package transcode

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSource is a closable ReadSeeker that records Close calls.
type fakeSource struct {
	closed atomic.Bool
}

func (f *fakeSource) Read(p []byte) (int, error)                { return 0, io.EOF }
func (f *fakeSource) Seek(off int64, whence int) (int64, error) { return 0, nil }
func (f *fakeSource) Close() error                              { f.closed.Store(true); return nil }

// stubCaps points the manager at a fake ffmpeg (sleeps; never writes) so
// launch() succeeds without a real encoder. Restored via ResetCachedForTesting.
func stubCaps(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	ffmpeg := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nsleep 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	SetCachedForTesting(&Capabilities{FFmpegPath: ffmpeg, Preferred: "libx264"})
	t.Cleanup(ResetCachedForTesting)
}

func lifecycleManager(t *testing.T) *HLSSessionManager {
	t.Helper()
	m, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	return m
}

// Concurrent GetOrStart calls for the same key must produce EXACTLY one
// session/encoder; every losing caller's source must be closed by the manager
// (this was the per-playlist-request FileReader leak + the double-ffmpeg
// TOCTOU writing into the same segment dir).
func TestGetOrStartConcurrentDedupe(t *testing.T) {
	stubCaps(t)
	m := lifecycleManager(t)

	const n = 8
	sources := make([]*fakeSource, n)
	sessions := make([]*HLSSession, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		sources[i] = &fakeSource{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := m.GetOrStart(context.Background(), HLSStartOpts{
				Key: "samekey", Source: sources[i], SourceSize: 1,
				KnownDurationSec: 10, // skip the ffprobe path
			})
			if err != nil {
				t.Errorf("GetOrStart[%d]: %v", i, err)
				return
			}
			sessions[i] = s
		}(i)
	}
	wg.Wait()

	for i := 1; i < n; i++ {
		if sessions[i] != sessions[0] {
			t.Fatalf("session %d differs from session 0 — duplicate encoders for the same key", i)
		}
	}
	m.mu.Lock()
	if len(m.sess) != 1 || len(m.starting) != 0 {
		t.Errorf("manager state: sess=%d starting=%d, want 1/0", len(m.sess), len(m.starting))
	}
	m.mu.Unlock()

	openCount := 0
	for i := range sources {
		if !sources[i].closed.Load() {
			openCount++
		}
	}
	if openCount != 1 {
		t.Errorf("%d sources left open, want exactly 1 (the session's own)", openCount)
	}

	// Closing the session must release the surviving source too.
	m.Close(sessions[0].Key)
	deadline := time.Now().Add(3 * time.Second)
	for openCount = countOpen(sources); openCount != 0 && time.Now().Before(deadline); openCount = countOpen(sources) {
		time.Sleep(20 * time.Millisecond)
	}
	if openCount != 0 {
		t.Errorf("after Close, %d sources still open, want 0", openCount)
	}
}

// Stop must reap every live session (closing its source) and be idempotent —
// it's registered as a graceful-shutdown cleanup so no ffmpeg is orphaned.
func TestManagerStopReapsSessionsAndIsIdempotent(t *testing.T) {
	stubCaps(t)
	m := lifecycleManager(t)

	src := &fakeSource{}
	s, err := m.GetOrStart(context.Background(), HLSStartOpts{
		Key: "k", Source: src, SourceSize: 1, KnownDurationSec: 10,
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	if s == nil {
		t.Fatal("nil session")
	}

	m.Stop()

	m.mu.Lock()
	n := len(m.sess)
	stopped := m.stopped
	m.mu.Unlock()
	if n != 0 {
		t.Errorf("after Stop, %d sessions remain, want 0", n)
	}
	if !stopped {
		t.Error("manager not marked stopped")
	}
	deadline := time.Now().Add(3 * time.Second)
	for !src.closed.Load() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !src.closed.Load() {
		t.Error("Stop must close the live session's source")
	}

	// Idempotent: a second Stop (e.g. cleanup runs twice) must not panic on the
	// already-closed stopCh.
	m.Stop()
}

func countOpen(sources []*fakeSource) int {
	n := 0
	for _, s := range sources {
		if !s.closed.Load() {
			n++
		}
	}
	return n
}

// An existing session must win and the caller's freshly-opened source must be
// closed (no caps needed — the hit path returns before any probing).
func TestGetOrStartClosesSourceOnExistingSession(t *testing.T) {
	m := lifecycleManager(t)
	existing := &HLSSession{Key: "k", LastAccess: time.Now()}
	m.mu.Lock()
	m.sess["k"] = existing
	m.mu.Unlock()

	src := &fakeSource{}
	s, err := m.GetOrStart(context.Background(), HLSStartOpts{Key: "k", Source: src})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	if s != existing {
		t.Fatal("should return the existing session")
	}
	if !src.closed.Load() {
		t.Error("abandoned source must be closed when the session already exists")
	}
}

// Creation failure (caps never probed) must close the handed-over source.
func TestGetOrStartClosesSourceOnError(t *testing.T) {
	ResetCachedForTesting()
	m := lifecycleManager(t)
	src := &fakeSource{}
	if _, err := m.GetOrStart(context.Background(), HLSStartOpts{Key: "k", Source: src}); err == nil {
		t.Fatal("want error with caps unprobed")
	}
	if !src.closed.Load() {
		t.Error("source must be closed on GetOrStart failure")
	}
	m.mu.Lock()
	if len(m.starting) != 0 {
		t.Errorf("starting slot leaked: %d entries", len(m.starting))
	}
	m.mu.Unlock()
}

// A stopped (reaped) session must refuse to relaunch — a stale pointer held by
// the segment handler used to resurrect ffmpeg into the removed directory.
func TestStoppedSessionRefusesRelaunch(t *testing.T) {
	s := &HLSSession{
		Key: "k", Dir: t.TempDir(),
		spec:   &encodeSpec{vod: true, ffmpegPath: "/bin/false"},
		source: &fakeSource{},
	}
	s.stop()

	if err := s.RestartAt(5); !errors.Is(err, errSessionStopped) {
		t.Errorf("RestartAt on dead session = %v, want errSessionStopped", err)
	}
	if err := s.launch(0); !errors.Is(err, errSessionStopped) {
		t.Errorf("launch on dead session = %v, want errSessionStopped", err)
	}
	if src := s.source.(*fakeSource); !src.closed.Load() {
		t.Error("stop() must close the session source")
	}
}
