package transcode

import (
	"context"
	"io"
)

// SourceForker is implemented by an HLS source that can hand out independent
// cursors over the same content (a torrent file: anacrolix readers are cheap
// and each carries its own readahead). The loopback source server then opens
// one cursor per Range request instead of serialising every request on the
// session's single cursor, so a request parked on a piece that has not arrived
// no longer holds up requests for bytes that are already there.
//
// ctx is the request context: implementations should abandon a blocked read
// when it is done (ffmpeg dropped the connection on a seek). The server closes
// the returned cursor when the request ends.
type SourceForker interface {
	ForkSource(ctx context.Context) (io.ReadSeekCloser, error)
}

// offsetReader reads len(p) bytes at an absolute offset with io.ReadFull
// semantics: n < len(p) always comes with a non-nil error.
type offsetReader interface {
	readAt(p []byte, off int64) (int, error)
}

// rangeSource hands one loopback request the reader it is served from, plus
// the release to call when the request ends.
type rangeSource interface {
	acquire(ctx context.Context) (offsetReader, func(), error)
}

// newRangeSource picks the cheapest correct way to serve concurrent Ranges
// from src: a private cursor per request, positional reads, or — for a plain
// single-cursor source — the locked Seek+Read of readSeekerContent.
func newRangeSource(src io.ReadSeeker) rangeSource {
	switch s := src.(type) {
	case SourceForker:
		return forkedSource{s}
	case io.ReaderAt:
		return readerAtSource{s}
	}
	return &readSeekerContent{ReadSeeker: src}
}

func (r *readSeekerContent) acquire(context.Context) (offsetReader, func(), error) {
	return r, func() {}, nil
}

type forkedSource struct{ forker SourceForker }

func (f forkedSource) acquire(ctx context.Context) (offsetReader, func(), error) {
	c, err := f.forker.ForkSource(ctx)
	if err != nil {
		return nil, nil, err
	}
	return &privateCursor{rs: c, pos: -1}, func() { _ = c.Close() }, nil
}

// privateCursor is a cursor owned by exactly one request goroutine, which is
// what keeps Seek+Read atomic without a lock: nobody else can move it between
// the two calls (the STSC/STCO corruption readSeekerContent guards against).
type privateCursor struct {
	rs  io.ReadSeeker
	pos int64 // offset the cursor is known to sit at; -1 = unknown
}

func (c *privateCursor) readAt(p []byte, off int64) (int, error) {
	if c.pos != off {
		c.pos = -1
		if _, err := c.rs.Seek(off, io.SeekStart); err != nil {
			return 0, err
		}
	}
	n, err := io.ReadFull(c.rs, p)
	if err != nil {
		c.pos = -1
		return n, err
	}
	c.pos = off + int64(n)
	return n, nil
}

// readerAtSource serves from an io.ReaderAt (*os.File for a completed
// download): positional reads carry their own offset, so concurrent requests
// share the source with no cursor and no lock.
type readerAtSource struct{ ra io.ReaderAt }

func (s readerAtSource) acquire(context.Context) (offsetReader, func(), error) {
	return s, func() {}, nil
}

func (s readerAtSource) readAt(p []byte, off int64) (int, error) {
	return s.ra.ReadAt(p, off)
}
