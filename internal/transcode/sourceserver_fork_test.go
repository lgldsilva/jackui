package transcode

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stallingCursor is a single-cursor reader (deliberately NOT an io.ReaderAt,
// like anacrolix's torrent.Reader) whose Read parks while the cursor sits in
// the stalled region — a piece the swarm has not delivered yet.
type stallingCursor struct {
	rs      io.ReadSeeker
	pos     int64
	src     *stallingSource
	closed  atomic.Bool
	ctxDone <-chan struct{}
}

func (c *stallingCursor) Seek(off int64, whence int) (int64, error) {
	pos, err := c.rs.Seek(off, whence)
	if err == nil {
		c.pos = pos
	}
	return pos, err
}

func (c *stallingCursor) Read(p []byte) (int, error) {
	if c.pos < c.src.stallBelow {
		c.src.enterOnce.Do(func() { close(c.src.entered) })
		select {
		case <-c.src.gate:
		case <-c.ctxDone:
			return 0, context.Canceled
		}
	}
	n, err := c.rs.Read(p)
	c.pos += int64(n)
	return n, err
}

func (c *stallingCursor) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		c.src.closes.Add(1)
	}
	return nil
}

// stallingSource is the session's primary source plus a fork factory. Reads
// below stallBelow block until gate is closed.
type stallingSource struct {
	*stallingCursor
	data       []byte
	stallBelow int64
	gate       chan struct{}
	entered    chan struct{}
	enterOnce  sync.Once
	forks      atomic.Int32
	closes     atomic.Int32
	forkErr    error
}

func newStallingSource(data []byte, stallBelow int64) *stallingSource {
	s := &stallingSource{
		data:       data,
		stallBelow: stallBelow,
		gate:       make(chan struct{}),
		entered:    make(chan struct{}),
	}
	s.stallingCursor = &stallingCursor{rs: bytes.NewReader(data), src: s}
	return s
}

func (s *stallingSource) ForkSource(ctx context.Context) (io.ReadSeekCloser, error) {
	if s.forkErr != nil {
		return nil, s.forkErr
	}
	s.forks.Add(1)
	return &stallingCursor{rs: bytes.NewReader(s.data), src: s, ctxDone: ctx.Done()}, nil
}

type rangeResult struct {
	body   []byte
	status int
	err    error
}

func fetchRangeCtx(ctx context.Context, url string, start, end int64) rangeResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return rangeResult{err: err}
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return rangeResult{err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return rangeResult{body: body, status: resp.StatusCode, err: err}
}

// TestSourceServerRangeNotBlockedByStalledRange pins the head-of-line fix: a
// Range request parked on a piece that has not arrived must not hold up a
// Range request for bytes that ARE available. With one shared cursor behind a
// mutex held across the blocking read, the second request could only be served
// after the first one's piece landed (ffmpeg seeking to `moov`/Cues while the
// abandoned sequential read was still parked).
func TestSourceServerRangeNotBlockedByStalledRange(t *testing.T) {
	const size = 1 << 20
	data := patternBytes(0, size)
	src := newStallingSource(data, 256<<10)
	var gateOnce sync.Once
	openGate := func() { gateOnce.Do(func() { close(src.gate) }) }
	t.Cleanup(openGate)

	srv, url, err := startSourceServer(HLSStartOpts{Source: src, SourceSize: size})
	if err != nil {
		t.Fatalf("startSourceServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	stalled := make(chan rangeResult, 1)
	go func() { stalled <- fetchRangeCtx(context.Background(), url, 0, 1023) }()
	select {
	case <-src.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("stalled request never reached the source read")
	}

	// The deadline is only a deadlock detector: on the fixed code this request
	// never waits on the stalled one, whatever the machine load.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const off = 600_000
	got := fetchRangeCtx(ctx, url, off, off+999)
	if got.err != nil {
		t.Fatalf("available range was blocked behind the stalled range: %v", got.err)
	}
	if got.status != http.StatusPartialContent || !bytes.Equal(got.body, patternBytes(off, 1000)) {
		t.Fatalf("available range: status=%d, %d bytes, content mismatch", got.status, len(got.body))
	}
	select {
	case r := <-stalled:
		t.Fatalf("stalled request finished before its data was released: %+v", r)
	default:
	}

	openGate()
	first := <-stalled
	if first.err != nil || first.status != http.StatusPartialContent || !bytes.Equal(first.body, patternBytes(0, 1024)) {
		t.Fatalf("stalled range after release: status=%d err=%v, %d bytes", first.status, first.err, len(first.body))
	}

	// Shutdown waits for the handlers, so every per-request cursor is closed.
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	if err := srv.Shutdown(sctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if f, c := src.forks.Load(), src.closes.Load(); f != 2 || c != 2 {
		t.Fatalf("want one cursor opened and closed per request (2/2), got forks=%d closes=%d", f, c)
	}
}

// A request whose client goes away must release its cursor: the fork is bound
// to the request context, so a read parked on a missing piece is abandoned
// instead of pinning a reader (and its readahead priorities) forever.
func TestSourceServerAbandonedRangeReleasesCursor(t *testing.T) {
	const size = 1 << 20
	src := newStallingSource(patternBytes(0, size), size)
	t.Cleanup(func() { close(src.gate) })
	srv, url, err := startSourceServer(HLSStartOpts{Source: src, SourceSize: size})
	if err != nil {
		t.Fatalf("startSourceServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan rangeResult, 1)
	go func() { done <- fetchRangeCtx(ctx, url, 0, 1023) }()
	<-src.entered
	cancel()
	<-done

	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	if err := srv.Shutdown(sctx); err != nil {
		t.Fatalf("handler still parked after the client left: %v", err)
	}
	if f, c := src.forks.Load(), src.closes.Load(); f != 1 || c != 1 {
		t.Fatalf("forks=%d closes=%d, want 1/1", f, c)
	}
}
