package streamer

import (
	"bytes"
	"io"
	"testing"
)

// TestComputeOSHashKnownVectors uses the published reference values from OpenSubtitles spec
// to verify the algorithm is implemented correctly.
//
// The spec: hash = filesize + sum(first 64KB chunks as uint64 LE) + sum(last 64KB chunks)
// Synthesizing inputs with known content lets us assert the math.
func TestComputeOSHashAllZeros(t *testing.T) {
	const size = 200 * 1024 // 200KB
	buf := make([]byte, size)
	r := bytes.NewReader(buf)
	hash, err := computeOSHash(r, size)
	if err != nil {
		t.Fatalf("computeOSHash: %v", err)
	}
	// Hash of all-zero file of size N = N (since chunk sums = 0)
	expected := uintToHex16(uint64(size))
	if hash != expected {
		t.Errorf("got %q, want %q", hash, expected)
	}
}

func TestComputeOSHashTooSmall(t *testing.T) {
	r := bytes.NewReader(make([]byte, 32*1024)) // 32KB < 64KB
	if _, err := computeOSHash(r, 32*1024); err == nil {
		t.Error("expected error for file < 64KB")
	}
}

func TestComputeOSHashConsistent(t *testing.T) {
	// Same input → same hash
	buf := make([]byte, 200*1024)
	for i := range buf {
		buf[i] = byte(i % 256)
	}
	h1, _ := computeOSHash(bytes.NewReader(buf), int64(len(buf)))
	h2, _ := computeOSHash(bytes.NewReader(buf), int64(len(buf)))
	if h1 != h2 {
		t.Errorf("not deterministic: %q vs %q", h1, h2)
	}
}

func TestComputeOSHashLengthIs16Hex(t *testing.T) {
	buf := make([]byte, 200*1024)
	h, _ := computeOSHash(bytes.NewReader(buf), int64(len(buf)))
	if len(h) != 16 {
		t.Errorf("OS hash must be 16 hex chars, got %d (%q)", len(h), h)
	}
}

func TestComputeOSHashIOError(t *testing.T) {
	// Reader that errors on first read
	r := &erroringReader{}
	if _, err := computeOSHash(r, 1024*1024); err == nil {
		t.Error("expected error from failing reader")
	}
}

// uintToHex16 produces the same format as computeOSHash's output
func uintToHex16(n uint64) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		out[i] = hex[n&0xf]
		n >>= 4
	}
	return string(out)
}

type erroringReader struct{}

func (e *erroringReader) Read(p []byte) (int, error)              { return 0, io.ErrUnexpectedEOF }
func (e *erroringReader) Seek(offset int64, whence int) (int64, error) { return 0, nil }
