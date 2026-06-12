package preview

import (
	"io"
	"sync"
)

// Source abstracts where the container bytes come from. Local files provide a
// real *os.File (ReaderAt for free); torrent files provide the anacrolix
// ReadSeeker wrapped by NewReaderAt. OpenSeq hands out a fresh reader
// positioned at byte 0 for the sequential formats (tar, rar) — for torrents
// that's a Seek(0) on the same underlying reader, which is fine because the
// preview handlers never interleave random and sequential access.
type Source struct {
	ReaderAt io.ReaderAt
	Size     int64
	OpenSeq  func() (io.ReadCloser, error)
}

// seekerAt adapts an io.ReadSeeker into an io.ReaderAt by serializing
// Seek+Read pairs under a mutex — same trick the transcode loopback server
// uses (STSC/STCO race). The torrent reader prioritizes pieces around the
// read position, so random access (zip central directory at EOF) works before
// the file finishes downloading.
type seekerAt struct {
	mu sync.Mutex
	rs io.ReadSeeker
}

// NewReaderAt wraps rs into a goroutine-safe io.ReaderAt.
func NewReaderAt(rs io.ReadSeeker) io.ReaderAt {
	return &seekerAt{rs: rs}
}

func (s *seekerAt) ReadAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.rs.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.ReadFull(s.rs, p)
	if err == io.ErrUnexpectedEOF {
		// ReadAt contract: short read at end of input reports io.EOF.
		err = io.EOF
	}
	return n, err
}

// nopCloser wraps a Reader with a no-op Close for OpenSeq implementations
// whose lifetime is managed by the caller (torrent readers closed once at the
// end of the request).
type nopCloser struct{ io.Reader }

func (nopCloser) Close() error { return nil }

type nopSeekCloser struct{ io.ReadSeeker }

func (nopSeekCloser) Close() error { return nil }

// NopCloser returns r as an io.ReadCloser with a no-op Close, preserving the
// Seeker interface when present (archive/tar skips entry bodies via Seek
// instead of reading them when its source can seek).
func NopCloser(r io.Reader) io.ReadCloser {
	if rs, ok := r.(io.ReadSeeker); ok {
		return nopSeekCloser{rs}
	}
	return nopCloser{r}
}

// readAllCapped reads at most capBytes bytes; if the stream has more, it
// returns ErrEntryTooLarge. THE zip-bomb guard: caps decompressed output no
// matter what sizes the archive headers declare.
func readAllCapped(r io.Reader, capBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, capBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > capBytes {
		return nil, ErrEntryTooLarge
	}
	return data, nil
}
