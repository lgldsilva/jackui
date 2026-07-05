package contentid

import (
	"bytes"
	"crypto/sha1"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// fp is a small helper: write content to a temp file and Fingerprint it.
func fp(t *testing.T, content []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h, err := Fingerprint(p, int64(len(content)))
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	return h
}

func TestFingerprint_IdenticalContentMatches(t *testing.T) {
	body := []byte("the very same bytes, twice over and then some padding")
	if a, b := fp(t, body), fp(t, body); a == "" || a != b {
		t.Fatalf("identical content must share a fingerprint: %q vs %q", a, b)
	}
}

func TestFingerprint_DifferentSizeDiffers(t *testing.T) {
	if a, b := fp(t, []byte("aaaa")), fp(t, []byte("aaaaaa")); a == b {
		t.Fatal("different sizes must never share a fingerprint")
	}
}

func TestFingerprint_LargeFileHeadTailSampling(t *testing.T) {
	big := make([]byte, 3*SampleBytes)
	for i := range big {
		big[i] = byte(i % 7)
	}
	base := fp(t, big)

	// Differ only in the HEAD → different fingerprint.
	head := append([]byte(nil), big...)
	head[0] ^= 0xFF
	if fp(t, head) == base {
		t.Fatal("a changed head must change the fingerprint")
	}

	// Differ only in the TAIL → different fingerprint.
	tail := append([]byte(nil), big...)
	tail[len(tail)-1] ^= 0xFF
	if fp(t, tail) == base {
		t.Fatal("a changed tail must change the fingerprint")
	}

	// Differ only in the MIDDLE → COLLIDES (documents the fingerprint's known
	// limitation: head + tail + size only, never the middle).
	mid := append([]byte(nil), big...)
	mid[len(mid)/2] ^= 0xFF
	if fp(t, mid) != base {
		t.Fatal("a middle-only change is expected to collide (head/tail fingerprint)")
	}
}

func TestFingerprintAt_NegativeSize(t *testing.T) {
	if _, err := FingerprintAt(bytes.NewReader([]byte("x")), -1); err == nil {
		t.Fatal("negative size must error")
	}
}

func TestFingerprintReadSeeker_MatchesFingerprintAt(t *testing.T) {
	// A torrent reader (ReadSeeker) and an on-disk file (ReaderAt) of identical
	// content MUST yield the same fingerprint — the basis for cross-source dedup.
	for _, n := range []int{10, SampleBytes, 2 * SampleBytes, 3 * SampleBytes, 5*SampleBytes + 17} {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte((i*13 + 2) % 251)
		}
		at, err1 := FingerprintAt(bytes.NewReader(data), int64(n))
		rs, err2 := FingerprintReadSeeker(bytes.NewReader(data), int64(n))
		if err1 != nil || err2 != nil {
			t.Fatalf("n=%d: errs %v / %v", n, err1, err2)
		}
		if at != rs || at == "" {
			t.Fatalf("n=%d: ReaderAt fp %q != ReadSeeker fp %q", n, at, rs)
		}
	}
}

func TestFingerprintReadSeeker_NegativeSize(t *testing.T) {
	if _, err := FingerprintReadSeeker(bytes.NewReader([]byte("x")), -1); err == nil {
		t.Fatal("negative size must error")
	}
}

func TestFileMatchesPieces(t *testing.T) {
	const pieceLen = 1024
	data := torrentStream(3 * pieceLen)
	pc := PieceCheck{PieceLen: pieceLen, FileStart: 0, FileLen: int64(len(data)), PieceHashes: sha1Pieces(data, pieceLen)}
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	if err := os.WriteFile(good, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if !FileMatchesPieces(good, pc) {
		t.Fatal("a byte-identical file must match")
	}
	bad := append([]byte(nil), data...)
	bad[10] ^= 0xFF
	badp := filepath.Join(dir, "bad")
	if err := os.WriteFile(badp, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if FileMatchesPieces(badp, pc) {
		t.Fatal("a corrupt file must not match")
	}
	if FileMatchesPieces(filepath.Join(dir, "missing"), pc) {
		t.Fatal("a missing file must not match")
	}
}

func TestFingerprintAt_ShortReaderErrors(t *testing.T) {
	// Declared size larger than the actual bytes → readAtFull short read → error.
	if _, err := FingerprintAt(bytes.NewReader([]byte("tiny")), 1000); err == nil {
		t.Fatal("a reader shorter than the declared size must error")
	}
}

func TestFingerprint_MissingFile(t *testing.T) {
	if _, err := Fingerprint(filepath.Join(t.TempDir(), "nope"), 10); err == nil {
		t.Fatal("a missing file must error")
	}
}

// sha1Pieces splits data into pieceLen-sized pieces and returns each piece's
// SHA-1, mirroring a v1 torrent's piece hash list.
func sha1Pieces(data []byte, pieceLen int64) [][]byte {
	var hs [][]byte
	for off := int64(0); off < int64(len(data)); off += pieceLen {
		end := off + pieceLen
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		s := sha1.Sum(data[off:end])
		hs = append(hs, append([]byte(nil), s[:]...))
	}
	return hs
}

func torrentStream(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte((i*31 + 7) % 251)
	}
	return data
}

func TestVerifyInteriorPieces_SingleFileAllMatch(t *testing.T) {
	const pieceLen = 1024
	data := torrentStream(10 * pieceLen) // exact multiple → every piece interior
	pc := PieceCheck{PieceLen: pieceLen, FileStart: 0, FileLen: int64(len(data)), PieceHashes: sha1Pieces(data, pieceLen)}
	interior, matched, err := VerifyInteriorPieces(bytes.NewReader(data), pc)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if interior != 10 || matched != 10 || !CertainMatch(interior, matched) {
		t.Fatalf("want 10/10 certain, got interior=%d matched=%d", interior, matched)
	}
}

func TestVerifyInteriorPieces_TrailingPartialPieceIsNotInterior(t *testing.T) {
	const pieceLen = 1024
	data := torrentStream(10*pieceLen + 50) // last piece is partial (50 bytes)
	pc := PieceCheck{PieceLen: pieceLen, FileStart: 0, FileLen: int64(len(data)), PieceHashes: sha1Pieces(data, pieceLen)}
	interior, matched, err := VerifyInteriorPieces(bytes.NewReader(data), pc)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if interior != 10 || matched != 10 {
		t.Fatalf("partial tail piece must be excluded: interior=%d matched=%d", interior, matched)
	}
}

func TestVerifyInteriorPieces_MultiFileUnalignedOffset(t *testing.T) {
	const pieceLen = 1024
	data := torrentStream(8 * pieceLen)
	fileStart := int64(2*pieceLen + 30) // unaligned: starts mid-piece
	fileLen := int64(3 * pieceLen)
	fileBytes := data[fileStart : fileStart+fileLen]
	pc := PieceCheck{PieceLen: pieceLen, FileStart: fileStart, FileLen: fileLen, PieceHashes: sha1Pieces(data, pieceLen)}

	interior, matched, err := VerifyInteriorPieces(bytes.NewReader(fileBytes), pc)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Interior pieces are 3 and 4 (piece 2 and piece 5 are boundary, shared).
	if interior != 2 || matched != 2 || !CertainMatch(interior, matched) {
		t.Fatalf("want 2/2 certain for unaligned file, got interior=%d matched=%d", interior, matched)
	}
}

func TestVerifyInteriorPieces_CorruptByteFailsMatch(t *testing.T) {
	const pieceLen = 1024
	data := torrentStream(5 * pieceLen)
	pc := PieceCheck{PieceLen: pieceLen, FileStart: 0, FileLen: int64(len(data)), PieceHashes: sha1Pieces(data, pieceLen)}
	corrupt := append([]byte(nil), data...)
	corrupt[2*pieceLen+10] ^= 0xFF // flip a byte inside interior piece 2
	interior, matched, err := VerifyInteriorPieces(bytes.NewReader(corrupt), pc)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if interior != 5 || matched != 4 || CertainMatch(interior, matched) {
		t.Fatalf("one corrupt piece must drop the match: interior=%d matched=%d", interior, matched)
	}
}

func TestVerifyInteriorPieces_FileSmallerThanPiece(t *testing.T) {
	pc := PieceCheck{PieceLen: 1024, FileStart: 0, FileLen: 500, PieceHashes: [][]byte{make([]byte, v1PieceHashLen)}}
	interior, matched, err := VerifyInteriorPieces(bytes.NewReader(make([]byte, 500)), pc)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if interior != 0 || matched != 0 || CertainMatch(interior, matched) {
		t.Fatalf("a sub-piece file has no interior pieces: interior=%d matched=%d", interior, matched)
	}
}

func TestVerifyInteriorPieces_ZeroPieceLen(t *testing.T) {
	interior, matched, err := VerifyInteriorPieces(bytes.NewReader([]byte("x")), PieceCheck{PieceLen: 0, FileLen: 10})
	if err != nil || interior != 0 || matched != 0 {
		t.Fatalf("degenerate PieceLen must be a no-op: interior=%d matched=%d err=%v", interior, matched, err)
	}
}

func TestVerifyInteriorPieces_ShortHashListErrors(t *testing.T) {
	const pieceLen = 1024
	data := torrentStream(4 * pieceLen)
	pc := PieceCheck{PieceLen: pieceLen, FileStart: 0, FileLen: int64(len(data)), PieceHashes: nil} // missing hashes
	if _, _, err := VerifyInteriorPieces(bytes.NewReader(data), pc); err == nil {
		t.Fatal("an interior piece without a hash must error")
	}
}

func TestVerifyInteriorPieces_StraddleOwnsNoWholePiece(t *testing.T) {
	// FileLen >= PieceLen but the file straddles a single boundary without fully
	// containing any piece (a small file sandwiched between two others in a
	// multi-file torrent). interiorPieces must report none.
	pc := PieceCheck{PieceLen: 10, FileStart: 5, FileLen: 10, PieceHashes: [][]byte{
		make([]byte, v1PieceHashLen), make([]byte, v1PieceHashLen), make([]byte, v1PieceHashLen),
	}}
	interior, matched, err := VerifyInteriorPieces(bytes.NewReader(make([]byte, 10)), pc)
	if err != nil || interior != 0 || matched != 0 {
		t.Fatalf("a straddling file owns no whole piece: interior=%d matched=%d err=%v", interior, matched, err)
	}
}

func TestVerifyInteriorPieces_ReadErrorPropagates(t *testing.T) {
	const pieceLen = 1024
	data := torrentStream(3 * pieceLen)
	pc := PieceCheck{PieceLen: pieceLen, FileStart: 0, FileLen: int64(len(data)), PieceHashes: sha1Pieces(data, pieceLen)}
	if _, _, err := VerifyInteriorPieces(errReaderAt{}, pc); err == nil {
		t.Fatal("a read error during verification must propagate")
	}
}

// errReaderAt fails every ReadAt — exercises the loop's read-error path.
type errReaderAt struct{}

func (errReaderAt) ReadAt([]byte, int64) (int, error) { return 0, io.ErrClosedPipe }

// shortNoErrReaderAt returns one byte short of the request with a nil error,
// exercising readAtFull's io.ErrUnexpectedEOF substitution (a plain bytes.Reader
// would instead return io.EOF and never hit that branch).
type shortNoErrReaderAt struct{}

func (shortNoErrReaderAt) ReadAt(p []byte, _ int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}

func TestFingerprintAt_ShortNoErrBecomesUnexpectedEOF(t *testing.T) {
	// Large path (size > 2*SampleBytes) so the head read goes through readAtFull
	// with a full-size buffer; the short-without-error read must surface as an error.
	_, err := FingerprintAt(shortNoErrReaderAt{}, 3*SampleBytes)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("short read without error must become ErrUnexpectedEOF, got %v", err)
	}
}

// headOkTailFails serves a full head at offset 0 but fails any later read,
// exercising the tail-read error branch of the large path.
type headOkTailFails struct{}

func (headOkTailFails) ReadAt(p []byte, off int64) (int, error) {
	if off == 0 {
		return len(p), io.EOF // full read; readAtFull treats this as success
	}
	return 0, io.ErrClosedPipe
}

func TestFingerprintAt_TailReadErrorPropagates(t *testing.T) {
	if _, err := FingerprintAt(headOkTailFails{}, 3*SampleBytes); err == nil {
		t.Fatal("a tail read error must propagate")
	}
}
