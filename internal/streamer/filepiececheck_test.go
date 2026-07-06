package streamer

import (
	"bytes"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/contentid"
)

// buildMultiPieceSpec builds an info-complete single-file torrent spec over data
// split into pieceLen-sized v1 pieces, so FilePieceCheck has real SHA-1 hashes.
func buildMultiPieceSpec(t *testing.T, data []byte, pieceLen int64) *torrent.TorrentSpec {
	t.Helper()
	var pieces []byte
	for off := int64(0); off < int64(len(data)); off += pieceLen {
		end := off + pieceLen
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		h := metainfo.HashBytes(data[off:end])
		pieces = append(pieces, h[:]...)
	}
	info := metainfo.Info{Name: "multi.bin", PieceLength: pieceLen, Length: int64(len(data)), Pieces: pieces}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("bencode.Marshal: %v", err)
	}
	spec, err := torrent.TorrentSpecFromMetaInfoErr(&metainfo.MetaInfo{InfoBytes: infoBytes})
	if err != nil {
		t.Fatalf("TorrentSpecFromMetaInfoErr: %v", err)
	}
	return spec
}

func activeMultiPiece(t *testing.T, data []byte, pieceLen int64) (*Streamer, metainfo.Hash) {
	t.Helper()
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("newTestStreamer: %v", err)
	}
	t.Cleanup(s.Close)
	tor, _, err := s.client.AddTorrentSpec(buildMultiPieceSpec(t, data, pieceLen))
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	hash := tor.InfoHash()
	s.mu.Lock()
	s.active[hash] = &entry{t: tor, lastAccess: time.Now()}
	s.mu.Unlock()
	return s, hash
}

func TestFilePieceCheck_BuildsV1CheckAndMatchesRealContent(t *testing.T) {
	const pieceLen = 1 << 14
	data := make([]byte, 5*pieceLen+123) // 5 full pieces + a partial tail piece
	for i := range data {
		data[i] = byte((i*7 + 3) % 251)
	}
	s, hash := activeMultiPiece(t, data, pieceLen)

	pc, v1, err := s.FilePieceCheck(hash, 0)
	if err != nil {
		t.Fatalf("FilePieceCheck: %v", err)
	}
	if !v1 {
		t.Fatal("a v1 single-file torrent must report v1=true")
	}
	if pc.PieceLen != pieceLen || pc.FileStart != 0 || pc.FileLen != int64(len(data)) {
		t.Fatalf("layout: pieceLen=%d start=%d len=%d", pc.PieceLen, pc.FileStart, pc.FileLen)
	}
	if len(pc.PieceHashes) != 6 { // 5 full + 1 partial
		t.Fatalf("want 6 global piece hashes, got %d", len(pc.PieceHashes))
	}

	// End-to-end: the real file content is a CERTAIN match (5 interior pieces).
	interior, matched, err := contentid.VerifyInteriorPieces(bytes.NewReader(data), pc)
	if err != nil {
		t.Fatalf("VerifyInteriorPieces: %v", err)
	}
	if interior != 5 || !contentid.CertainMatch(interior, matched) {
		t.Fatalf("real content must be a certain match: interior=%d matched=%d", interior, matched)
	}

	// A single corrupted byte inside an interior piece breaks the certainty.
	bad := append([]byte(nil), data...)
	bad[pieceLen+9] ^= 0xFF
	_, m2, err := contentid.VerifyInteriorPieces(bytes.NewReader(bad), pc)
	if err != nil {
		t.Fatalf("VerifyInteriorPieces(bad): %v", err)
	}
	if contentid.CertainMatch(interior, m2) {
		t.Fatalf("a corrupted candidate must not be a certain match (matched=%d)", m2)
	}
}

func TestFilePieceCheck_NotActive(t *testing.T) {
	s := NewForTesting() // no Close(): the lightweight test streamer has no worker channels
	if _, _, err := s.FilePieceCheck(metainfo.Hash{}, 0); err == nil {
		t.Fatal("expected error for a torrent that isn't active")
	}
}

func TestFilePieceCheck_FileIndexOutOfRange(t *testing.T) {
	s, hash := activeMultiPiece(t, bytes.Repeat([]byte("a"), 1<<14), 1<<14)
	if _, _, err := s.FilePieceCheck(hash, 99); err == nil {
		t.Fatal("expected error for an out-of-range file index")
	}
}
