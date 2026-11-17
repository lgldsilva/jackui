package transcode

import "sync"

// runStderrTailBytes caps the ffmpeg stderr kept for failure diagnostics.
const runStderrTailBytes = 16 << 10

// tailBuffer is an io.Writer that keeps only the last `max` bytes written.
// ffmpeg's stderr on a multi-hour progressive transcode is unbounded, and only
// its tail is ever read (failure diagnostics).
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) >= b.max {
		b.buf = append(b.buf[:0], p[len(p)-b.max:]...)
		return len(p), nil
	}
	if over := len(b.buf) + len(p) - b.max; over > 0 {
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
