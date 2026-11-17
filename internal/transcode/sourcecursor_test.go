package transcode

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestSourceServerForkFailureIs503(t *testing.T) {
	src := newStallingSource([]byte("0123456789"), 0)
	src.forkErr = errors.New("torrent not active")
	rs := newRangeSource(src)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/source", nil)
	r.Header.Set("Range", "bytes=2-5")
	serveSource(w, r, rs, 10)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if cl := w.Header().Get("Content-Range"); cl != "" {
		t.Errorf("error response must not advertise a range, got %q", cl)
	}

	// HEAD never needs a cursor, so it still answers.
	w = httptest.NewRecorder()
	serveSource(w, httptest.NewRequest(http.MethodHead, "/source", nil), rs, 10)
	if w.Code != http.StatusOK || w.Header().Get("Content-Length") != "10" {
		t.Fatalf("HEAD: status=%d len=%q", w.Code, w.Header().Get("Content-Length"))
	}
	if src.forks.Load() != 0 {
		t.Errorf("HEAD opened a cursor")
	}
}

func TestNewRangeSourcePicksReader(t *testing.T) {
	if _, ok := newRangeSource(newStallingSource(nil, 0)).(forkedSource); !ok {
		t.Error("a SourceForker must get per-request cursors")
	}
	if _, ok := newRangeSource(bytes.NewReader(nil)).(readerAtSource); !ok {
		t.Error("an io.ReaderAt must be read positionally")
	}
	plain := struct{ io.ReadSeeker }{bytes.NewReader(nil)}
	if _, ok := newRangeSource(plain).(*readSeekerContent); !ok {
		t.Error("a plain single-cursor source must keep the locked Seek+Read")
	}
}

func TestReaderAtSourceServesConcurrentRanges(t *testing.T) {
	const size = 512 << 10
	rs := newRangeSource(bytes.NewReader(patternBytes(0, size)))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(start int64) {
			defer wg.Done()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/source", nil)
			r.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, start+4999))
			serveSource(w, r, rs, size)
			if w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), patternBytes(start, 5000)) {
				t.Errorf("range at %d: status=%d, %d bytes, content mismatch", start, w.Code, w.Body.Len())
			}
		}(int64(i) * 60_000)
	}
	wg.Wait()

	// Past-EOF read: the bytes that exist are written, then the copy stops.
	w := httptest.NewRecorder()
	serveSource(w, httptest.NewRequest(http.MethodGet, "/source", nil), rs, size+100)
	if w.Body.Len() != size {
		t.Errorf("short source: wrote %d bytes, want %d", w.Body.Len(), size)
	}
}

type failingSeeker struct{ io.Reader }

func (failingSeeker) Seek(int64, int) (int64, error) { return 0, errors.New("seek failed") }
func (failingSeeker) Close() error                   { return nil }

func TestPrivateCursorSeeksOnlyWhenOffsetMoves(t *testing.T) {
	counting := &seekCounter{ReadSeeker: bytes.NewReader(patternBytes(0, 4096))}
	c := &privateCursor{rs: counting, pos: -1}
	buf := make([]byte, 1000)
	for _, off := range []int64{0, 1000, 2000} { // sequential: one positioning seek
		if n, err := c.readAt(buf, off); err != nil || n != 1000 || !bytes.Equal(buf, patternBytes(off, 1000)) {
			t.Fatalf("readAt(%d) = %d, %v", off, n, err)
		}
	}
	if counting.seeks != 1 {
		t.Errorf("sequential reads seeked %d times, want 1", counting.seeks)
	}
	if _, err := c.readAt(buf, 100); err != nil || !bytes.Equal(buf, patternBytes(100, 1000)) {
		t.Fatalf("readAt after jump: %v", err)
	}
	if counting.seeks != 2 {
		t.Errorf("a jump must reposition: seeks=%d, want 2", counting.seeks)
	}
	// A short read leaves the cursor position unknown: the next read reseeks.
	if n, err := c.readAt(buf, 3500); err == nil || n != 596 {
		t.Fatalf("short read = %d, %v", n, err)
	}
	if _, err := c.readAt(buf, 0); err != nil || counting.seeks != 4 {
		t.Fatalf("after a short read: err=%v seeks=%d, want 4", err, counting.seeks)
	}

	bad := &privateCursor{rs: failingSeeker{bytes.NewReader(nil)}, pos: -1}
	if _, err := bad.readAt(buf, 10); err == nil {
		t.Error("a seek failure must surface")
	}
}

type seekCounter struct {
	io.ReadSeeker
	seeks int
}

func (s *seekCounter) Seek(off int64, whence int) (int64, error) {
	s.seeks++
	return s.ReadSeeker.Seek(off, whence)
}
