package streamer

import (
	"context"
	"io"
	"sync"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// forkState parks the parent cursor the first time it is forked.
type forkState struct{ park sync.Once }

// ForkSource opens an independent cursor over the same torrent file, bound to
// ctx (transcode.SourceForker). The HLS loopback server opens one per Range
// request, so a request parked on a missing piece no longer blocks the others
// behind the single cursor's lock.
//
// It is deliberately lighter than FileReader: the per-file verify and the tail
// warm-up already ran when the parent was opened. Once forked, the parent only
// keeps the session's source alive — it must not be read any more.
func (r *trackingReader) ForkSource(ctx context.Context) (io.ReadSeekCloser, error) {
	if r.file == nil || !r.streamer.isActiveTorrent(r.hash, r.file.Torrent()) {
		return nil, ErrTorrentNotActive
	}
	r.forks.park.Do(r.park)
	nr := r.file.NewReader()
	nr.SetReadahead(r.window)
	nr.SetResponsive()
	// Reader.Close() does not wake a Read parked in waitAvailable; the context
	// does, so a request ffmpeg abandoned on a seek releases its cursor.
	nr.SetContext(ctx)
	return &forkedReader{trackingReader: &trackingReader{
		Reader:   nr,
		streamer: r.streamer,
		hash:     r.hash,
		file:     r.file,
		window:   r.window,
	}}, nil
}

// park stops the idle parent from competing with its forks: it would otherwise
// keep the head of the file prioritised (readahead + Now window at offset 0)
// for the whole session, however far playback has moved.
func (r *trackingReader) park() {
	r.Reader.SetReadahead(0)
	r.releaseWindow()
}

// releaseWindow demotes this cursor's Now window; the tail (High) is a
// per-file hint and stays.
func (r *trackingReader) releaseWindow() {
	if r.file == nil || r.last.empty() {
		return
	}
	applyPieceWindow(r.file.Torrent(), r.last, pieceWindow{highBegin: r.last.highBegin, highEnd: r.last.highEnd})
	r.last = pieceWindow{}
}

// forkedReader is a per-request cursor. Closing it gives its window back, the
// same way a seek on the single cursor used to demote the window it left.
type forkedReader struct{ *trackingReader }

func (f *forkedReader) Close() error {
	f.releaseWindow()
	return f.Reader.Close()
}

// isActiveTorrent reports whether t is still the live torrent registered under
// hash (not dropped, not replaced by a re-add).
func (s *Streamer) isActiveTorrent(hash metainfo.Hash, t *torrent.Torrent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.active[hash]
	return ok && e.t == t
}
