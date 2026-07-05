package streamer

import (
	"errors"
	"fmt"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/contentid"
)

// FilePieceCheck builds the data contentid.VerifyInteriorPieces needs to confirm
// that a candidate file is byte-identical to file fileIdx of this ACTIVE torrent,
// using the torrent's own v1 (BEP3) SHA-1 piece hashes. The bool is false for a
// v2-only (BEP52) torrent — it has no v1 piece hashes, so the caller must fall
// back to a fingerprint comparison. Used by cross-torrent dedup (#23) to adopt an
// already-present file instead of re-downloading it.
func (s *Streamer) FilePieceCheck(hash metainfo.Hash, fileIdx int) (contentid.PieceCheck, bool, error) {
	s.mu.Lock()
	e, ok := s.active[hash]
	s.mu.Unlock()
	if !ok {
		return contentid.PieceCheck{}, false, ErrTorrentNotActive
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return contentid.PieceCheck{}, false, fmt.Errorf(errFileIndexOutOfRange, fileIdx)
	}
	info := e.t.Info()
	if info == nil {
		return contentid.PieceCheck{}, false, errors.New("torrent info not available yet")
	}
	f := files[fileIdx]
	pc := contentid.PieceCheck{
		PieceLen:  info.PieceLength,
		FileStart: f.Offset(),
		FileLen:   f.Length(),
	}
	// v2-only torrents carry no v1 SHA-1 piece hashes → signal a fingerprint fallback.
	n := info.NumPieces()
	if !info.HasV1() || n <= 0 || len(info.Pieces) < n*metainfo.HashSize {
		return pc, false, nil
	}
	hashes := make([][]byte, n)
	for i := 0; i < n; i++ {
		// Sub-slice of the stable Pieces backing array (read-only downstream);
		// the three-index slice caps len and cap to exactly one 20-byte hash.
		hashes[i] = info.Pieces[i*metainfo.HashSize : (i+1)*metainfo.HashSize : (i+1)*metainfo.HashSize]
	}
	pc.PieceHashes = hashes
	return pc, true, nil
}

// FingerprintFile returns the contentid Fingerprint (size + head/tail) of file
// fileIdx of this ACTIVE torrent, reading only the ends through the torrent
// reader (a few pieces from the swarm). Used by cross-torrent dedup (#23) to
// match a torrent file against a cloud/library file by content — the "probable"
// path for remote candidates that can't be cheaply piece-verified.
func (s *Streamer) FingerprintFile(hash metainfo.Hash, fileIdx int) (string, error) {
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		e.lastAccess = time.Now()
	}
	files := []*torrent.File{}
	if ok {
		files = e.t.Files()
	}
	s.mu.Unlock()
	if !ok {
		return "", ErrTorrentNotActive
	}
	if fileIdx < 0 || fileIdx >= len(files) {
		return "", fmt.Errorf(errFileIndexOutOfRange, fileIdx)
	}
	f := files[fileIdx]
	r := f.NewReader()
	r.SetReadahead(contentid.SampleBytes)
	r.SetResponsive()
	defer r.Close()
	return contentid.FingerprintReadSeeker(r, f.Length())
}
