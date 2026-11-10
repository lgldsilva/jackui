package transcode

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// Regression (#200): WaitForMaster/WaitForSegment ignored the request context,
// so a client that disconnected kept its handler goroutine (and LastAccess
// refreshes that pinned the session) for the full timeout.
func TestWaitFor_ReturnsOnCanceledContext(t *testing.T) {
	s := &HLSSession{Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	if err := s.WaitForMaster(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForMaster err = %v, want context.Canceled", err)
	}
	if _, err := s.WaitForSegment(ctx, "seg_00000.ts", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForSegment err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("waits took %s after cancel — context ignored", d)
	}
}

// Regression (#200): after Stop() (shutdown) the gcLoop is gone, yet
// GetOrStart still launched encoders that nothing would ever reap.
func TestGetOrStart_AfterStopRefusesAndClosesSource(t *testing.T) {
	stubCaps(t)
	m := lifecycleManager(t)
	m.Stop()

	src := &fakeSource{}
	_, err := m.GetOrStart(context.Background(), HLSStartOpts{Key: "after-stop", Source: src})
	if !errors.Is(err, ErrManagerStopped) {
		t.Fatalf("GetOrStart after Stop err = %v, want ErrManagerStopped", err)
	}
	if !src.closed.Load() {
		t.Fatal("source must be closed when the manager refuses")
	}
	if n := len(m.sess); n != 0 {
		t.Fatalf("manager holds %d sessions after Stop", n)
	}
}

// Regression (#200): a failed loopback-server start left the freshly created
// session directory behind.
func TestBuildSession_SourceServerFailureRemovesDir(t *testing.T) {
	SetCachedForTesting(&Capabilities{Preferred: "libx264"})
	defer ResetCachedForTesting()
	m, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	const key = "0123456789abcdef0123456789abcdef01234567"
	if _, err := m.buildSession(context.Background(), key, HLSStartOpts{Source: nil}); err == nil {
		t.Fatal("buildSession without a source must fail")
	}
	dir, derr := m.sessionDir(key)
	if derr != nil {
		t.Fatal(derr)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("session dir left behind after a failed build: %v", err)
	}
}
