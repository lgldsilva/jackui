package streamer

import (
	"os"
	"path/filepath"
	"testing"
)

// Verifies that the cache size accounting reports only the bytes actually
// allocated on disk, not the logical placeholder size. This is the property
// users complained about: a 10 GB sparse file with only a tiny prefix written
// must NOT count as 10 GB toward the eviction cap.
func TestDirSizeReportsPhysicalNotLogical(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "huge_sparse"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Truncate to a logical size much larger than what we actually write.
	const logicalSize int64 = 1 << 30 // 1 GiB
	if err := f.Truncate(logicalSize); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	// Write a tiny amount of real data.
	if _, err := f.Write([]byte("hello sparse world")); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	got, _, err := dirSizeAndMTime(dir)
	if err != nil {
		t.Fatalf("dirSizeAndMTime: %v", err)
	}
	// The logical size is 1 GiB but we only touched ~18 bytes. The allocated
	// blocks should be at most one or two filesystem blocks (typically 4 KiB
	// each). A real sparse filesystem returns a few KB at most. Anything close
	// to logicalSize means we're still reporting the wrong number.
	if got >= logicalSize/2 {
		t.Errorf("dirSizeAndMTime returned %d bytes — too close to logical %d (sparse not detected)", got, logicalSize)
	}
	if got <= 0 {
		t.Errorf("dirSizeAndMTime returned %d bytes — should be > 0 since we wrote data", got)
	}
}
