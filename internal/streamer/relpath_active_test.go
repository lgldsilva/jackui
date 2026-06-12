package streamer

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// #26: when neither the metadata cache nor a cached .torrent knows the file, the
// rel path must still resolve off an already-active torrent — so a finished pack
// reactivated for playback serves from disk instead of re-downloading.
func TestFileRelPath_FromActiveTorrent(t *testing.T) {
	const pieceLen = 1 << 14
	s, hash := activeMultiPiece(t, make([]byte, 2*pieceLen), pieceLen)

	if got := s.FileRelPath(hash, 0); got != "multi.bin" {
		t.Fatalf("FileRelPath(0) off the active torrent = %q, want multi.bin", got)
	}
	// Out-of-range index on the active torrent → empty.
	if got := s.FileRelPath(hash, 9); got != "" {
		t.Fatalf("FileRelPath(9) = %q, want empty", got)
	}
	// A hash that isn't active → empty (no cache, no metainfo, not active).
	if got := s.FileRelPath(metainfo.Hash{}, 0); got != "" {
		t.Fatalf("inactive hash = %q, want empty", got)
	}
}
