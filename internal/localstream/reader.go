// Package localstream wraps the io.ReadSeeker of a local mounted file so the
// server can do two things at once while a video plays:
//
//   - measure how fast bytes are actually pulled from the underlying mount
//     (a throughput indicator for the UI — crucial on rclone/Google-Drive
//     mounts where a play silently fetches over the network), and
//   - read ahead in large, aligned blocks so ffmpeg/ServeContent don't issue
//     many tiny Range reads that an rclone FUSE mount penalises.
//
// A single Session covers both the direct-play path (it is an io.ReadSeeker,
// fed to http.ServeContent) and the HLS path (the transcode package wraps it
// in its own atomic readSeekerContent). The Registry keys sessions so the
// transfer-status endpoint can find the live throughput for a given file.
package localstream

import (
	"errors"
	"io"
	"sync"
	"time"
)

// ReadSeekCloser is the underlying file handle a Session owns (an *os.File
// satisfies it). The Session closes it when the Registry reaps the session.
type ReadSeekCloser interface {
	io.ReadSeeker
	io.Closer
}

// defaults tuned for rclone/Drive: a 16 MiB read-ahead amortises the per-read
// network round-trip; a 3 s rate window is responsive without being jumpy; a
// session counts as "active" if it pulled bytes in the last 5 s.
const (
	defaultReadaheadBytes = 16 << 20
	defaultRateWindow     = 3 * time.Second
	defaultActiveTTL      = 5 * time.Second
)

// Snapshot is the immutable view the transfer-status endpoint serialises.
type Snapshot struct {
	Key        string `json:"key"`
	BytesRead  int64  `json:"bytesRead"`
	RatePerSec int64  `json:"ratePerSec"`
	Size       int64  `json:"size"`
	Active     bool   `json:"active"`
	Stalled    bool   `json:"stalled"`
}

type sample struct {
	t time.Time
	n int64
}

// Session wraps one open file. Read/Seek are mutex-guarded so it is safe to
// share across the (already serialised) HLS reader and concurrent status
// reads. Only bytes fetched from the underlying file are metered — buffer
// replays are not, so the rate reflects real mount I/O, not cache hits.
type Session struct {
	key        string
	readaheadN int
	rateWindow time.Duration
	activeTTL  time.Duration
	now        func() time.Time

	mu   sync.Mutex
	rs   ReadSeekCloser
	size int64
	pos  int64 // logical cursor exposed to callers

	// underlyingPos tracks the real file cursor so we skip redundant Seeks.
	underlyingPos int64

	// read-ahead buffer: buf[:bufLen] holds the file bytes starting at bufStart.
	buf      []byte
	bufStart int64
	bufLen   int

	totalRead  int64
	lastReadAt time.Time
	samples    []sample
	closed     bool
}

func newSession(key string, rs ReadSeekCloser, size int64, readaheadBytes int, nowFn func() time.Time) *Session {
	if readaheadBytes <= 0 {
		readaheadBytes = defaultReadaheadBytes
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	return &Session{
		key:        key,
		readaheadN: readaheadBytes,
		rateWindow: defaultRateWindow,
		activeTTL:  defaultActiveTTL,
		now:        nowFn,
		rs:         rs,
		size:       size,
	}
}

// Read serves from the read-ahead buffer when possible; otherwise it refills
// the buffer with one large aligned read at the current position. Implements
// io.Reader.
func (s *Session) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if s.size > 0 && s.pos >= s.size {
		return 0, io.EOF
	}
	if !s.posInBuffer(s.pos) {
		if err := s.fillAt(s.pos); err != nil {
			return 0, err
		}
	}
	off := int(s.pos - s.bufStart)
	n := copy(p, s.buf[off:s.bufLen])
	s.pos += int64(n)
	return n, nil
}

// Seek moves the logical cursor. A seek that lands outside the current
// read-ahead buffer invalidates it (the next Read refills). SeekEnd is used by
// http.ServeContent for sizing and never meters or refills. Implements
// io.Seeker.
func (s *Session) Seek(offset int64, whence int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = s.pos + offset
	case io.SeekEnd:
		abs = s.size + offset
	default:
		return 0, errors.New("localstream: invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("localstream: negative position")
	}
	s.pos = abs
	if !s.posInBuffer(abs) {
		s.bufLen = 0
	}
	return abs, nil
}

func (s *Session) posInBuffer(pos int64) bool {
	return s.bufLen > 0 && pos >= s.bufStart && pos < s.bufStart+int64(s.bufLen)
}

// fillAt reads up to readaheadN bytes from the underlying file starting at off
// into the buffer, metering the bytes actually pulled. Called with s.mu held.
func (s *Session) fillAt(off int64) error {
	if s.underlyingPos != off {
		if _, err := s.rs.Seek(off, io.SeekStart); err != nil {
			return err
		}
		s.underlyingPos = off
	}
	if cap(s.buf) < s.readaheadN {
		s.buf = make([]byte, s.readaheadN)
	}
	n, err := io.ReadFull(s.rs, s.buf[:s.readaheadN])
	if n > 0 {
		s.underlyingPos += int64(n)
		s.bufStart = off
		s.bufLen = n
		s.record(int64(n))
	}
	// A short fill near EOF is expected, not an error — we still serve the n
	// bytes we got. Only a zero-byte read at EOF is terminal.
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		if n == 0 {
			return io.EOF
		}
		return nil
	}
	return err
}

func (s *Session) record(n int64) {
	now := s.now()
	s.totalRead += n
	s.lastReadAt = now
	s.samples = append(s.samples, sample{t: now, n: n})
	s.prune(now)
}

// prune drops samples older than the rate window. Called with s.mu held.
func (s *Session) prune(now time.Time) {
	cutoff := now.Add(-s.rateWindow)
	i := 0
	for i < len(s.samples) && !s.samples[i].t.After(cutoff) {
		i++
	}
	if i > 0 {
		s.samples = append(s.samples[:0], s.samples[i:]...)
	}
}

// Snapshot returns the current throughput view. Safe to call concurrently.
func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.prune(now)
	var sum int64
	for _, sm := range s.samples {
		sum += sm.n
	}
	rate := int64(float64(sum) / s.rateWindow.Seconds())
	active := !s.lastReadAt.IsZero() && now.Sub(s.lastReadAt) < s.activeTTL
	stalled := !active && s.totalRead > 0 && (s.size <= 0 || s.totalRead < s.size)
	return Snapshot{
		Key:        s.key,
		BytesRead:  s.totalRead,
		RatePerSec: rate,
		Size:       s.size,
		Active:     active,
		Stalled:    stalled,
	}
}

// idleFor reports how long since the last metered read (for the reaper).
func (s *Session) idleFor(now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastReadAt.IsZero() {
		return 0 // never read yet — give it a chance to start
	}
	return now.Sub(s.lastReadAt)
}

// close releases the underlying file handle once.
func (s *Session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	_ = s.rs.Close()
}
