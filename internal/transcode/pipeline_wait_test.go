package transcode

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stallAfterReader serves `head` bytes and then blocks forever, ignoring any
// context — the worst-case source (a reader that cannot be interrupted).
type stallAfterReader struct {
	head  int
	block chan struct{}
}

func (r *stallAfterReader) Read(p []byte) (int, error) {
	if r.head <= 0 {
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
	in := &stallAfterReader{head: 512 * 1024, block: make(chan struct{})}
	t.Cleanup(func() { close(in.block) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, in, httptest.NewRecorder(), Options{}) }()

	time.Sleep(300 * time.Millisecond)
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
