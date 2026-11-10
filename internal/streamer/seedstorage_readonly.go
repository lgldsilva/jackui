package streamer

import (
	"context"
	"errors"
	"sync"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// errRelocatedReadOnly is returned for every chunk write into a relocated file.
var errRelocatedReadOnly = errors.New("relocated storage is read-only")

// readOnlyClient wraps the relocated (outside-DataDir) storage so anacrolix can
// only READ and verify the files that live there, never write to them. Those
// paths come from the downloads DB (completed bulk files, possibly dedup-linked
// or renamed); a piece that fails verification must not be "repaired" by peers
// writing straight into a file we do not own. Verified pieces still seed; failed
// ones stay incomplete. anacrolix's default chunk-write-error handler disables
// data download for the torrent on the first refused write, so it stops
// requesting instead of looping.
//
// Only pieces overlapping a relocated file are locked: files the resolver did
// not map fall back to DataDir and remain writable.
type readOnlyClient struct {
	inner     storage.ClientImpl
	relocated *fileIndexSet
}

func (c readOnlyClient) OpenTorrent(ctx context.Context, info *metainfo.Info, h metainfo.Hash) (storage.TorrentImpl, error) {
	t, err := c.inner.OpenTorrent(ctx, info, h)
	if err != nil {
		return t, err
	}
	// The inner OpenTorrent ran the FilePathMaker, so the relocated set is final.
	locked := lockedPieces(info, c.relocated)
	wrap := func(p metainfo.Piece, pi storage.PieceImpl) storage.PieceImpl {
		if i := p.Index(); i >= 0 && i < len(locked) && locked[i] {
			return readOnlyPiece{pi}
		}
		return pi
	}
	if t.Piece != nil {
		inner := t.Piece
		t.Piece = func(p metainfo.Piece) storage.PieceImpl { return wrap(p, inner(p)) }
	}
	if t.PieceWithHash != nil {
		inner := t.PieceWithHash
		t.PieceWithHash = func(p metainfo.Piece, ph g.Option[[]byte]) storage.PieceImpl {
			return wrap(p, inner(p, ph))
		}
	}
	return t, nil
}

// Close forwards to the wrapped storage when it owns resources.
func (c readOnlyClient) Close() error {
	if cl, ok := c.inner.(interface{ Close() error }); ok {
		return cl.Close()
	}
	return nil
}

// lockedPieces marks every piece whose byte range overlaps a relocated file.
// A nil set locks nothing.
func lockedPieces(info *metainfo.Info, relocated *fileIndexSet) []bool {
	locked := make([]bool, info.NumPieces())
	pl := info.PieceLength
	if pl <= 0 || relocated == nil {
		return locked
	}
	var off int64
	for i, f := range info.UpvertedFiles() {
		if f.Length > 0 && relocated.has(i) {
			for p := off / pl; p <= (off+f.Length-1)/pl && p < int64(len(locked)); p++ {
				locked[p] = true
			}
		}
		off += f.Length
	}
	return locked
}

// readOnlyPiece refuses writes; reads, completion and MarkComplete pass through
// (MarkComplete only records a piece that already hash-checked).
type readOnlyPiece struct{ storage.PieceImpl }

func (readOnlyPiece) WriteAt([]byte, int64) (int, error) { return 0, errRelocatedReadOnly }

// fileIndexSet is a small concurrency-safe set of torrent file indices.
type fileIndexSet struct {
	mu  sync.Mutex
	idx map[int]struct{}
}

func newFileIndexSet() *fileIndexSet { return &fileIndexSet{idx: map[int]struct{}{}} }

func (s *fileIndexSet) add(i int) {
	s.mu.Lock()
	s.idx[i] = struct{}{}
	s.mu.Unlock()
}

func (s *fileIndexSet) has(i int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.idx[i]
	return ok
}
