package downloads

import (
	"crypto/sha1"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/contentid"
)

func TestTryLinkExisting_NilDeps(t *testing.T) {
	// The nil-deps guard must short-circuit before dereferencing the file.
	w := &Worker{}
	if w.tryLinkExisting(&Download{UserID: 1, InfoHash: "h"}, metainfo.Hash{}, 0, nil) {
		t.Fatal("nil store/streamer must not link")
	}
}

// pieceCheckOver builds a PieceCheck over data split into pieceLen v1 pieces,
// with the file at offset 0 (so every full piece is interior).
func pieceCheckOver(data []byte, pieceLen int64) contentid.PieceCheck {
	var hs [][]byte
	for off := int64(0); off < int64(len(data)); off += pieceLen {
		end := off + pieceLen
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		s := sha1.Sum(data[off:end])
		hs = append(hs, append([]byte(nil), s[:]...))
	}
	return contentid.PieceCheck{PieceLen: pieceLen, FileStart: 0, FileLen: int64(len(data)), PieceHashes: hs}
}

func writeTmp(t *testing.T, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cand.bin")
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestCertainFileMatch(t *testing.T) {
	data := make([]byte, 3*1024)
	for i := range data {
		data[i] = byte((i*5 + 1) % 251)
	}
	pc := pieceCheckOver(data, 1024)

	if !certainFileMatch(writeTmp(t, data), pc) {
		t.Fatal("identical content must be a certain match")
	}
	bad := append([]byte(nil), data...)
	bad[1500] ^= 0xFF
	if certainFileMatch(writeTmp(t, bad), pc) {
		t.Fatal("corrupted content must not match")
	}
	if certainFileMatch(filepath.Join(t.TempDir(), "missing"), pc) {
		t.Fatal("a missing file must not match")
	}
}

func TestLinkMatch_LinksByteIdenticalCandidate(t *testing.T) {
	s := newTestStore(t)
	data := make([]byte, 4*1024)
	for i := range data {
		data[i] = byte((i*3 + 7) % 251)
	}
	path := writeTmp(t, data)
	size := int64(len(data))

	// Seed a completed file for user 1 (a different torrent) pointing at `path`.
	if _, err := s.CreateLinked(Download{UserID: 1, InfoHash: "orig", FileIndex: 0, Magnet: "m", Name: "Orig"}, path, size); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}

	w := &Worker{store: s}
	newDL := &Download{UserID: 1, InfoHash: "newhash", FileIndex: 0, Magnet: "magnet:newhash", Name: "New"}
	if !w.linkMatch(newDL, size, pieceCheckOver(data, 1024)) {
		t.Fatal("a byte-identical candidate must be linked")
	}
	// The new download is now a completed link pointing at the existing file.
	got, err := s.GetByKey(1, "newhash", 0)
	if err != nil || got == nil {
		t.Fatalf("linked row not found: %v", err)
	}
	if !got.Linked || got.Status != StatusCompleted || got.FilePath != path {
		t.Fatalf("not linked correctly: linked=%v status=%q path=%q", got.Linked, got.Status, got.FilePath)
	}
}

func TestLinkMatch_SkipsSameTorrentAndMismatch(t *testing.T) {
	s := newTestStore(t)
	data := make([]byte, 3*1024)
	for i := range data {
		data[i] = byte(i % 251)
	}
	pc := pieceCheckOver(data, 1024)
	size := int64(len(data))
	w := &Worker{store: s}

	// User 2: the only candidate of this size is from the SAME torrent → skipped.
	if _, err := s.CreateLinked(Download{UserID: 2, InfoHash: "h", FileIndex: 0, Magnet: "m"}, writeTmp(t, data), size); err != nil {
		t.Fatalf("seed same-torrent: %v", err)
	}
	if w.linkMatch(&Download{UserID: 2, InfoHash: "h", FileIndex: 0}, size, pc) {
		t.Fatal("a candidate from the same torrent must be skipped")
	}

	// User 3: a same-size candidate with DIFFERENT content → not a certain match.
	diff := append([]byte(nil), data...)
	diff[100] ^= 0xFF
	if _, err := s.CreateLinked(Download{UserID: 3, InfoHash: "orig", FileIndex: 0, Magnet: "m"}, writeTmp(t, diff), size); err != nil {
		t.Fatalf("seed mismatch: %v", err)
	}
	if w.linkMatch(&Download{UserID: 3, InfoHash: "newhash", FileIndex: 0, Magnet: "m"}, size, pc) {
		t.Fatal("a same-size but different-content candidate must not link")
	}

	// Empty: no candidate of this size for user 9 → false.
	if w.linkMatch(&Download{UserID: 9, InfoHash: "x", FileIndex: 0}, size, pc) {
		t.Fatal("no candidate must mean no link")
	}
}
