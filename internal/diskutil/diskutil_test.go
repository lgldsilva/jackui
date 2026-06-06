package diskutil

import (
	"os"
	"testing"
)

func TestUsage_RealPath(t *testing.T) {
	free, total := Usage(os.TempDir())
	if total <= 0 {
		t.Fatalf("total = %d, want > 0 for a real filesystem", total)
	}
	if free < 0 || free > total {
		t.Errorf("free = %d out of range (total %d)", free, total)
	}
}

func TestUsage_BadPath(t *testing.T) {
	free, total := Usage("/this/path/should/not/exist/anywhere-xyz")
	if free != 0 || total != 0 {
		t.Errorf("bad path = (%d, %d), want (0, 0)", free, total)
	}
}
