package handlers

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/transcode"
)

// overlapSource mimics the torrent reader on a source-limited stream: it
// serves `head` bytes, then parks until its context is canceled, and takes a
// moment to unwind after the wake-up (anacrolix's reset-and-retry chain). Like
// that reader, its Close and Read share unsynchronized state, so the race
// detector flags any overlap; the atomic flags make it fail without -race too.
type overlapSource struct {
	head   int
	unwind time.Duration
	parked chan struct{} // closed when Read first parks on ctx; nil = not observed
	once   sync.Once

	mu  sync.Mutex
	ctx context.Context

	state       int // deliberately unguarded: Read and Close both write it
	inRead      atomic.Bool
	closed      atomic.Bool
	overlapped  atomic.Bool
	readOnClose atomic.Bool
	closes      atomic.Int32
}

func (s *overlapSource) SetContext(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
}

func (s *overlapSource) Read(p []byte) (int, error) {
	s.inRead.Store(true)
	defer s.inRead.Store(false)
	if s.closed.Load() {
		s.readOnClose.Store(true)
	}
	s.state++
	if s.head > 0 {
		n := min(len(p), s.head)
		s.head -= n
		return n, nil
	}
	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()
	if s.parked != nil {
		s.once.Do(func() { close(s.parked) })
	}
	<-ctx.Done()
	time.Sleep(s.unwind)
	s.state++
	return 0, ctx.Err()
}

func (s *overlapSource) Close() error {
	if s.inRead.Load() {
		s.overlapped.Store(true)
	}
	s.closed.Store(true)
	s.closes.Add(1)
	s.state = -1
	return nil
}

func (s *overlapSource) assertClean(t *testing.T) {
	t.Helper()
	if s.overlapped.Load() {
		t.Error("Close ran while a Read was still in flight")
	}
	if s.readOnClose.Load() {
		t.Error("the source was read after it had been closed")
	}
	if n := s.closes.Load(); n != 1 {
		t.Errorf("source closed %d times, want exactly 1", n)
	}
}

func stubTranscodeFFmpeg(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg-stub")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil { // #nosec G306 -- test stub must be executable
		t.Fatal(err)
	}
	transcode.SetCachedForTesting(&transcode.Capabilities{FFmpegPath: path})
	t.Cleanup(transcode.ResetCachedForTesting)
}

func runTranscodeAsync(ctx context.Context, src io.ReadCloser) <-chan error {
	done := make(chan error, 1)
	go func() { done <- runTorrentTranscode(ctx, src, httptest.NewRecorder(), transcode.Options{}) }()
	return done
}

// TestRunTorrentTranscode_CloseWaitsForPump is the regression test for the
// reader being closed while the unjoined stdin pump was still inside Read: the
// client leaves mid-stream, ffmpeg is killed, and the source must only be
// closed once the pump's last Read has returned.
func TestRunTorrentTranscode_CloseWaitsForPump(t *testing.T) {
	stubTranscodeFFmpeg(t, "exec cat >/dev/null")
	src := &overlapSource{head: 512 * 1024, unwind: 200 * time.Millisecond, parked: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	done := runTranscodeAsync(ctx, src)
	select {
	case <-src.parked: // pump is inside Read, the state the regression needs
	case <-time.After(5 * time.Second):
		t.Fatal("stdin pump never parked in the source Read")
	}
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("nil error for a killed ffmpeg")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still blocked 5s after the client left")
	}
	src.assertClean(t)
}

// TestRunTorrentTranscode_FFmpegExitsFirst: ffmpeg quits on its own while the
// request is still live and the pump is parked in the source. Teardown must
// wake that Read itself instead of waiting for the client to go away.
func TestRunTorrentTranscode_FFmpegExitsFirst(t *testing.T) {
	// The stub drains exactly the pre-warm block, so the pump has delivered
	// everything and is parked in the source when ffmpeg exits.
	stubTranscodeFFmpeg(t, "head -c 262144 >/dev/null")
	src := &overlapSource{head: 256 * 1024}

	select {
	case err := <-runTranscodeAsync(context.Background(), src):
		if err != nil {
			t.Errorf("err = %v, want nil (ffmpeg exited 0)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still blocked 5s after ffmpeg exited: nothing wakes the parked source read")
	}
	src.assertClean(t)
}

func TestGuardedReader(t *testing.T) {
	src := &overlapSource{head: 4}
	g := &guardedReader{r: src}

	buf := make([]byte, 8)
	if n, err := g.Read(buf); n != 4 || err != nil {
		t.Fatalf("Read = (%d, %v), want (4, nil)", n, err)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if n, err := g.Read(buf); n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Errorf("Read after Close = (%d, %v), want (0, os.ErrClosed)", n, err)
	}
	if err := g.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	src.assertClean(t)
}
