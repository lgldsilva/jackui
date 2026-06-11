// Package contentid derives identities for files so the SAME content can be
// recognised across torrents and mounts without re-downloading it. Two flavours,
// matched to the two situations the dedup-on-add feature (#23) faces:
//
//   - Fingerprint — a PROBABLE match. Hashes size + the first and last
//     SampleBytes, never the middle, so on an rclone/Drive (FUSE) mount it costs
//     two small ranged reads instead of fetching the whole object. Cheap enough
//     to run against cloud files; strong enough to SUGGEST "you already have
//     this". It is a fingerprint, not a full hash: files sharing size + head +
//     tail but differing only in the middle collide (astronomically unlikely for
//     real media, but never trusted for a silent auto-link).
//
//   - VerifyInteriorPieces — an EXACT match for local files. Re-hashes the
//     candidate against a v1 torrent's own SHA-1 piece hashes for every piece
//     that lies fully inside the file. Zero false positives (it is the torrent's
//     own integrity check), at the cost of reading the file's bytes — so it is
//     reserved for local candidates, never the cloud.
package contentid

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// SampleBytes is how much is read from each end of a file for a Fingerprint.
const SampleBytes = 64 << 10 // 64 KiB

// v1PieceHashLen is the length of a single SHA-1 piece hash in a BEP3 torrent.
const v1PieceHashLen = sha1.Size // 20

// Fingerprint opens abs and returns its Fingerprint (see package doc). size is
// the file's declared length; it is folded into the hash so length alone always
// distinguishes, and it bounds the head/tail reads.
func Fingerprint(abs string, size int64) (string, error) {
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return FingerprintAt(f, size)
}

// FingerprintAt is Fingerprint over an already-open io.ReaderAt of the given
// size. Files at or under 2*SampleBytes are hashed whole (already tiny).
func FingerprintAt(ra io.ReaderAt, size int64) (string, error) {
	return fingerprint(size, func(buf []byte, off int64) error { return readAtFull(ra, buf, off) })
}

// FingerprintReadSeeker is Fingerprint over an io.ReadSeeker (e.g. a torrent
// file reader, which is not a ReaderAt). It writes the exact same bytes to the
// hash as FingerprintAt, so a torrent file and an on-disk file of identical
// content yield the SAME fingerprint — the basis for matching a torrent's file
// against a cloud/library file without a piece-by-piece check.
func FingerprintReadSeeker(rs io.ReadSeeker, size int64) (string, error) {
	return fingerprint(size, func(buf []byte, off int64) error {
		if _, err := rs.Seek(off, io.SeekStart); err != nil {
			return err
		}
		_, err := io.ReadFull(rs, buf)
		return err
	})
}

// fingerprint hashes size + head + tail (or the whole file when ≤ 2*SampleBytes),
// reading through readFull(buf, off) so both the ReaderAt and ReadSeeker variants
// feed the hash an identical byte sequence.
func fingerprint(size int64, readFull func(buf []byte, off int64) error) (string, error) {
	if size < 0 {
		return "", fmt.Errorf("contentid: negative size %d", size)
	}
	h := sha256.New()
	fmt.Fprintf(h, "%d:", size)
	if size <= 2*SampleBytes {
		buf := make([]byte, size)
		if err := readFull(buf, 0); err != nil {
			return "", err
		}
		h.Write(buf)
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	head := make([]byte, SampleBytes)
	if err := readFull(head, 0); err != nil {
		return "", err
	}
	h.Write(head)
	tail := make([]byte, SampleBytes)
	if err := readFull(tail, size-SampleBytes); err != nil {
		return "", err
	}
	h.Write(tail)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// PieceCheck describes where a single file sits inside a v1 torrent's piece grid.
//
// v1-ONLY: PieceHashes must be 20-byte SHA-1 hashes (BEP3). A v2 (BEP52) torrent
// uses per-file SHA-256 merkle hashes that are not comparable here — callers must
// detect v2 and fall back to Fingerprint instead of calling VerifyInteriorPieces
// (which returns an error if handed non-20-byte hashes for an interior piece).
type PieceCheck struct {
	PieceLen    int64    // torrent piece length (must be > 0)
	FileStart   int64    // byte offset of the file within the torrent's concatenated stream
	FileLen     int64    // length of the file in bytes
	PieceHashes [][]byte // every piece's 20-byte SHA-1 hash, indexed by global piece index
}

// interiorPieces returns the [first,last] inclusive range of global piece
// indices that lie ENTIRELY within the file: a piece i is interior when
// [i*PieceLen, (i+1)*PieceLen) ⊆ [FileStart, FileStart+FileLen). ok is false when
// the file is shorter than one piece (or the layout is degenerate), so there is
// nothing this method can prove on its own.
func (pc PieceCheck) interiorPieces() (first, last int, ok bool) {
	if pc.PieceLen <= 0 || pc.FileLen < pc.PieceLen {
		return 0, 0, false
	}
	end := pc.FileStart + pc.FileLen
	first = int((pc.FileStart + pc.PieceLen - 1) / pc.PieceLen) // first multiple of PieceLen ≥ FileStart
	last = int(end/pc.PieceLen) - 1                             // highest i with (i+1)*PieceLen ≤ end
	if last < first {
		return 0, 0, false
	}
	return first, last, true
}

// VerifyInteriorPieces re-hashes the candidate file (read through ra, where
// offset 0 is the start of the file) against the torrent's own SHA-1 piece
// hashes for every piece fully inside the file. It returns how many interior
// pieces exist and how many matched. interior > 0 && matched == interior means
// the file's interior bytes are byte-identical to what the torrent expects — an
// exact match with no false positives (use CertainMatch). Boundary pieces shared
// with neighbouring files are skipped: they can't be judged from this file alone.
func VerifyInteriorPieces(ra io.ReaderAt, pc PieceCheck) (interior, matched int, err error) {
	first, last, ok := pc.interiorPieces()
	if !ok {
		return 0, 0, nil
	}
	buf := make([]byte, pc.PieceLen)
	for i := first; i <= last; i++ {
		if i >= len(pc.PieceHashes) || len(pc.PieceHashes[i]) != v1PieceHashLen {
			return interior, matched, fmt.Errorf("contentid: missing or short piece hash at index %d", i)
		}
		interior++
		off := int64(i)*pc.PieceLen - pc.FileStart
		if rerr := readAtFull(ra, buf, off); rerr != nil {
			return interior, matched, rerr
		}
		sum := sha1.Sum(buf)
		if bytes.Equal(sum[:], pc.PieceHashes[i]) {
			matched++
		}
	}
	return interior, matched, nil
}

// CertainMatch reports an exact identity from a VerifyInteriorPieces result: at
// least one interior piece existed and every one matched.
func CertainMatch(interior, matched int) bool {
	return interior > 0 && interior == matched
}

// FileMatchesPieces reports whether the file at path is byte-identical to the
// torrent file described by pc, verifying every interior piece. A missing or
// unreadable file, or one with no interior pieces, is not a match.
func FileMatchesPieces(path string, pc PieceCheck) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	interior, matched, err := VerifyInteriorPieces(f, pc)
	return err == nil && CertainMatch(interior, matched)
}

// readAtFull fills buf entirely from ra at off. ReadAt is allowed to return
// io.EOF alongside a full read, so a full read is success regardless of err; a
// short read without an error becomes io.ErrUnexpectedEOF.
func readAtFull(ra io.ReaderAt, buf []byte, off int64) error {
	n, err := ra.ReadAt(buf, off)
	if n == len(buf) {
		return nil
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return err
}
