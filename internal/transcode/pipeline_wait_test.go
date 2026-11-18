package transcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// stallAfterReader serves `head` bytes and then blocks forever, ignoring any
// context — the worst-case source (a reader that cannot be interrupted).
type stallAfterReader struct {
	head   int
	block  chan struct{}
	parked chan struct{} // closed when Read first parks; nil = not observed
	once   sync.Once
}

func (r *stallAfterReader) Read(p []byte) (int, error) {
	if r.head <= 0 {
		if r.parked != nil {
			r.once.Do(func() { close(r.parked) })
		}
		<-r.block
		return 0, io.EOF
	}
	n := min(len(p), r.head)
	r.head -= n
	return n, nil
}

func stubFFmpeg(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg-stub")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRun_ReturnsAfterCancelDespiteStalledStdin: once ffmpeg is killed by the
// canceled context, Run must not wait forever on the stdin copy goroutine that
// is parked in a source Read which never returns.
func TestRun_ReturnsAfterCancelDespiteStalledStdin(t *testing.T) {
	transSwapCache(t, &Capabilities{FFmpegPath: stubFFmpeg(t, "exec cat >/dev/null")})
	in := &stallAfterReader{head: 512 * 1024, block: make(chan struct{}), parked: make(chan struct{})}
	t.Cleanup(func() { close(in.block) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, in, httptest.NewRecorder(), Options{}) }()

	// Cancel only once the stdin pump is provably parked in the stalled Read
	// (the scenario under test), not after a guessed 300ms.
	select {
	case <-in.parked:
	case <-time.After(5 * time.Second):
		t.Fatal("stdin pump never reached the stalled Read")
	}
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("Run returned nil for a killed ffmpeg")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run still blocked 5s after cancel: Run is pinned by the stalled source read")
	}
}

// TestRun_FailureReportsStderrTail keeps the diagnostic contract after the
// stderr buffer became bounded: the last stderr line still reaches the error.
func TestRun_FailureReportsStderrTail(t *testing.T) {
	transSwapCache(t, &Capabilities{FFmpegPath: stubFFmpeg(t, "cat >/dev/null; echo first >&2; echo 'boom: bad input' >&2; exit 3")})

	err := Run(context.Background(), strings.NewReader("payload"), httptest.NewRecorder(), Options{})
	if err == nil || !strings.Contains(err.Error(), "boom: bad input") {
		t.Fatalf("err = %v, want the last stderr line", err)
	}
}

func TestFeedStdin_RejectsPresetStdin(t *testing.T) {
	cmd := exec.Command("/bin/true")
	cmd.Stdin = strings.NewReader("already set")
	if err := feedStdin(cmd, strings.NewReader("x")); err == nil {
		t.Fatal("feedStdin accepted a command whose Stdin was already set")
	}
}

func TestTailBuffer(t *testing.T) {
	b := newTailBuffer(8)
	if got := b.String(); got != "" {
		t.Errorf("empty buffer = %q", got)
	}
	for _, chunk := range []string{"abc", "def", "ghij"} {
		if n, err := b.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = (%d, %v)", chunk, n, err)
		}
	}
	if got := b.String(); got != "cdefghij" {
		t.Errorf("tail = %q, want %q", got, "cdefghij")
	}
	// A single write larger than the cap keeps only its own tail.
	if _, err := b.Write([]byte("0123456789ABCDEF")); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "89ABCDEF" {
		t.Errorf("tail after oversized write = %q, want %q", got, "89ABCDEF")
	}
}

func TestExpectedPumpEnd(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, true},
		{fmt.Errorf("write |1: %w", syscall.EPIPE), true},
		{&os.PathError{Op: "write", Path: "|1", Err: os.ErrClosed}, true},
		{fmt.Errorf("read: %w", context.Canceled), true},
		{errors.New("piece hash mismatch"), false},
		{io.ErrUnexpectedEOF, false},
	} {
		if got := expectedPumpEnd(tc.err); got != tc.want {
			t.Errorf("expectedPumpEnd(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

type failAfterReader struct {
	head int
	err  error
}

func (r *failAfterReader) Read(p []byte) (int, error) {
	if r.head <= 0 {
		return 0, r.err
	}
	n := min(len(p), r.head)
	r.head -= n
	return n, nil
}

// syncBuffer lets the test read log output the pump goroutine writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestRun_LogsMidStreamSourceFailure: ffmpeg gets a clean EOF and exits 0 on
// a source that broke mid-stream, so the pump's log line is the only trace.
func TestRun_LogsMidStreamSourceFailure(t *testing.T) {
	transSwapCache(t, &Capabilities{FFmpegPath: stubFFmpeg(t, "exec cat >/dev/null")})
	logs := &syncBuffer{}
	prev := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(prev) })

	in := &failAfterReader{head: 512 * 1024, err: errors.New("piece hash mismatch")}
	if err := Run(context.Background(), in, httptest.NewRecorder(), Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logs.String(), "source read failed mid-stream: piece hash mismatch") {
		if time.Now().After(deadline) {
			t.Fatalf("source failure was not logged; log output:\n%s", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
