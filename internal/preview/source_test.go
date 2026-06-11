package preview

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestNewReaderAt(t *testing.T) {
	ra := NewReaderAt(bytes.NewReader([]byte("0123456789")))

	buf := make([]byte, 4)
	n, err := ra.ReadAt(buf, 3)
	if err != nil || n != 4 || string(buf) != "3456" {
		t.Errorf("ReadAt(3) = %q, n=%d, err=%v", buf, n, err)
	}

	// Short read at the end must report io.EOF per the ReaderAt contract.
	n, err = ra.ReadAt(buf, 8)
	if !errors.Is(err, io.EOF) || n != 2 || string(buf[:n]) != "89" {
		t.Errorf("ReadAt(8) = %q, n=%d, err=%v, want EOF after 2 bytes", buf[:n], n, err)
	}

	// Reads are independent of each other (seek state serialized).
	n, err = ra.ReadAt(buf[:2], 0)
	if err != nil || n != 2 || string(buf[:2]) != "01" {
		t.Errorf("ReadAt(0) = %q, n=%d, err=%v", buf[:2], n, err)
	}
}

func TestNopCloserPreservesSeeker(t *testing.T) {
	rc := NopCloser(strings.NewReader("abc"))
	if _, ok := rc.(io.Seeker); !ok {
		t.Error("NopCloser over a ReadSeeker should preserve Seeker (tar skip optimization)")
	}
	if err := rc.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	plain := NopCloser(io.LimitReader(strings.NewReader("abc"), 2))
	if _, ok := plain.(io.Seeker); ok {
		t.Error("NopCloser over a plain Reader must not claim Seeker")
	}
	if err := plain.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestReadAllCapped(t *testing.T) {
	got, err := readAllCapped(strings.NewReader("12345"), 5)
	if err != nil || string(got) != "12345" {
		t.Errorf("exact cap: %q, %v", got, err)
	}
	if _, err := readAllCapped(strings.NewReader("123456"), 5); !errors.Is(err, ErrEntryTooLarge) {
		t.Errorf("over cap err = %v, want ErrEntryTooLarge", err)
	}
}
