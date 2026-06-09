package localstream

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"
)

// spyFile wraps a bytes.Reader to record the size of each underlying Read and
// to satisfy io.Closer. It lets a test assert that the read-ahead coalesced
// many small consumer reads into a few large mount reads.
type spyFile struct {
	*bytes.Reader
	mu     sync.Mutex
	reads  []int
	closed bool
}

func newSpyFile(data []byte) *spyFile { return &spyFile{Reader: bytes.NewReader(data)} }

func (s *spyFile) Read(p []byte) (int, error) {
	n, err := s.Reader.Read(p)
	s.mu.Lock()
	if n > 0 {
		s.reads = append(s.reads, n)
	}
	s.mu.Unlock()
	return n, err
}

func (s *spyFile) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *spyFile) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reads)
}

// clock is an injectable monotonic-ish source for deterministic rate tests.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func makeData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

func TestSessionReadSequentialMatchesAndCoalesces(t *testing.T) {
	data := makeData(4 << 20) // 4 MiB
	spy := newSpyFile(data)
	s := newSession("k", spy, int64(len(data)), 1<<20, nil) // 1 MiB read-ahead

	got := make([]byte, 0, len(data))
	buf := make([]byte, 64<<10) // consumer reads in 64 KiB chunks
	for {
		n, err := s.Read(buf)
		got = append(got, buf[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("content mismatch: got %d bytes", len(got))
	}
	// 4 MiB at 1 MiB read-ahead → ~4 underlying reads, not ~64.
	if c := spy.readCount(); c > 6 {
		t.Fatalf("expected coalesced reads (≤6), got %d", c)
	}
	if snap := s.Snapshot(); snap.BytesRead != int64(len(data)) {
		t.Fatalf("BytesRead=%d want %d", snap.BytesRead, len(data))
	}
}

func TestSessionSeekWithinBufferSkipsUnderlyingRead(t *testing.T) {
	data := makeData(2 << 20)
	spy := newSpyFile(data)
	s := newSession("k", spy, int64(len(data)), 1<<20, nil)

	buf := make([]byte, 4096)
	if _, err := s.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	firstReads := spy.readCount()
	// Seek backwards inside the already-buffered first MiB → no new fill.
	if _, err := s.Seek(1024, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if _, err := s.Read(buf); err != nil {
		t.Fatalf("Read after seek: %v", err)
	}
	if spy.readCount() != firstReads {
		t.Fatalf("in-buffer seek triggered an underlying read: %d→%d", firstReads, spy.readCount())
	}
}

func TestSessionSeekEndDoesNotMeter(t *testing.T) {
	data := makeData(1 << 20)
	spy := newSpyFile(data)
	s := newSession("k", spy, int64(len(data)), 1<<20, nil)

	end, err := s.Seek(0, io.SeekEnd)
	if err != nil {
		t.Fatalf("Seek end: %v", err)
	}
	if end != int64(len(data)) {
		t.Fatalf("SeekEnd=%d want %d", end, len(data))
	}
	if snap := s.Snapshot(); snap.BytesRead != 0 || spy.readCount() != 0 {
		t.Fatalf("SeekEnd metered bytes: bytesRead=%d reads=%d", snap.BytesRead, spy.readCount())
	}
}

func TestSessionReadPastEndReturnsEOF(t *testing.T) {
	data := makeData(100)
	s := newSession("k", newSpyFile(data), int64(len(data)), 1<<20, nil)
	buf := make([]byte, 200)
	n, _ := s.Read(buf)
	if n != 100 {
		t.Fatalf("first read n=%d want 100", n)
	}
	if _, err := s.Read(buf); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestSessionSeekErrors(t *testing.T) {
	s := newSession("k", newSpyFile(makeData(10)), 10, 1<<20, nil)
	if _, err := s.Seek(0, 99); err == nil {
		t.Fatal("expected error for invalid whence")
	}
	if _, err := s.Seek(-5, io.SeekStart); err == nil {
		t.Fatal("expected error for negative position")
	}
}

func TestSessionSnapshotRateAndActive(t *testing.T) {
	data := makeData(3 << 20)
	clk := newClock()
	s := newSession("k", newSpyFile(data), int64(len(data)), 1<<20, clk.now)

	buf := make([]byte, 1<<20)
	// Pull 1 MiB at t0, advance 1s, pull another 1 MiB.
	if _, err := s.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	clk.advance(1 * time.Second)
	if _, err := s.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	snap := s.Snapshot()
	if !snap.Active {
		t.Fatal("expected Active right after a read")
	}
	// 2 MiB pulled inside the 3s window → ~2 MiB / 3s.
	wantApprox := int64(2<<20) / 3
	if snap.RatePerSec < wantApprox/2 || snap.RatePerSec > 2<<20 {
		t.Fatalf("RatePerSec=%d out of expected band (~%d)", snap.RatePerSec, wantApprox)
	}

	// Let the session go quiet past the active TTL → inactive, and stalled
	// because not all bytes have been read.
	clk.advance(10 * time.Second)
	snap = s.Snapshot()
	if snap.Active {
		t.Fatal("expected inactive after activeTTL")
	}
	if !snap.Stalled {
		t.Fatal("expected stalled (idle with bytes remaining)")
	}
	if snap.RatePerSec != 0 {
		t.Fatalf("expected rate 0 after window emptied, got %d", snap.RatePerSec)
	}
}

func TestSessionConcurrentAccessIsRaceFree(t *testing.T) {
	data := makeData(8 << 20)
	s := newSession("k", newSpyFile(data), int64(len(data)), 256<<10, nil)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			buf := make([]byte, 32<<10)
			for i := 0; i < 50; i++ {
				_, _ = s.Seek(int64((seed*7+i*131)%len(data)), io.SeekStart)
				_, _ = s.Read(buf)
				_ = s.Snapshot()
			}
		}(g)
	}
	wg.Wait()
}
