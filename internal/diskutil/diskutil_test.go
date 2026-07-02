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

func TestParentDisk(t *testing.T) {
	cases := map[string]string{
		"sdc1":      "sdc",
		"sda12":     "sda",
		"vdb1":      "vdb",
		"nvme0n1p2": "nvme0n1",
		"mmcblk0p1": "mmcblk0",
		"sdc":       "", // already a whole disk
		"nvme0n1":   "", // whole disk (no pN)
		"nvme0n1p":  "", // malformed → no parent
	}
	for in, want := range cases {
		if got := parentDisk(in); got != want {
			t.Errorf("parentDisk(%q) = %q, want %q", in, got, want)
		}
	}
}

// Smoke: IsRotational must never panic and a non-existent path is non-rotational
// (false) — the safe fallback that allows parallelism.
func TestIsRotational_Smoke(t *testing.T) {
	if IsRotational("/no/such/path/xyz-123") {
		t.Error("non-existent path should be non-rotational (false)")
	}
	_ = IsRotational(os.TempDir()) // must not panic; value depends on hardware
}
