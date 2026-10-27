package handlers

import (
	"context"
	"sync"
	"testing"
	"time"
)

// leaseBlockingReader mimics an anacrolix torrent reader: a Read blocked on a
// missing piece ONLY unblocks when a context is wired in via SetContext
// (waitAvailable returns ctx.Err()). Reader.Close() does not unblock it — that
// is exactly the anacrolix behavior documented at streamer_io.go (Prefetch) and
// streamer_verify.go (warmTail).
type leaseBlockingReader struct {
	mu     sync.Mutex
	ctx    context.Context
	closed bool
	setCtx chan context.Context
}

func newLeaseBlockingReader() *leaseBlockingReader {
	return &leaseBlockingReader{ctx: context.Background(), setCtx: make(chan context.Context, 1)}
}

func (r *leaseBlockingReader) SetContext(ctx context.Context) {
	select {
	case r.setCtx <- ctx:
	default:
	}
	r.mu.Lock()
	r.ctx = ctx
	r.mu.Unlock()
}

func (r *leaseBlockingReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	ctx := r.ctx
	r.mu.Unlock()
	<-ctx.Done() // nothing else can unblock the read — same as waitAvailable
	return 0, ctx.Err()
}

func (r *leaseBlockingReader) Seek(int64, int) (int64, error) { return 0, nil }

func (r *leaseBlockingReader) Close() error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	return nil
}

// TestReadTorrentTags_TimeoutWiresCtxIntoReaderAndCloses pins the goroutine+
// reader leak fix: on timeout the read deadline must be wired INTO the reader
// (SetContext) so the blocked read — and the goroutine holding it — ends, and
// the reader gets closed. Before the fix the goroutine stayed blocked in the
// read forever (Close only runs after the read returns), leaking one
// goroutine+reader+buffer per card on a stalled swarm.
func TestReadTorrentTags_TimeoutWiresCtxIntoReaderAndCloses(t *testing.T) {
	rc := newLeaseBlockingReader()

	start := time.Now()
	tags, ok := readTorrentTags(context.Background(), rc, 50*time.Millisecond)
	if ok {
		t.Fatal("expected the timeout path (ok=false) on a never-delivered piece")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout path took %s, want ~50ms", elapsed)
	}
	if tags.Artist != "" || tags.Title != "" {
		t.Fatal("timeout must return empty tags")
	}

	// The read deadline must have been wired into the reader itself.
	select {
	case got := <-rc.setCtx:
		if got == nil {
			t.Fatal("SetContext was called with a nil context")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readTorrentTags never wired a context into the reader — the read goroutine leaks on a stalled reader")
	}

	// After the timeout the goroutine must unblock via the wired ctx and close
	// the reader. Before the fix this never happens: the read stays blocked.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rc.mu.Lock()
		closed := rc.closed
		rc.mu.Unlock()
		if closed {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("reader was never closed after the timeout — goroutine+reader left blocked forever (retention leak)")
}
