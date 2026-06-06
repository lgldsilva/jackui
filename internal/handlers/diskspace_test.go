package handlers

import (
	"os"
	"testing"

	"github.com/luizg/jackui/internal/local"
)

func TestMountsWithSpace(t *testing.T) {
	mounts := []local.Mount{
		{Name: "tmp", Path: os.TempDir()},
		{Name: "broken", Path: "/nope/missing-xyz"},
	}
	out := mountsWithSpace(mounts)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].Name != "tmp" || out[0].TotalBytes <= 0 {
		t.Errorf("tmp mount: %+v, want name=tmp total>0", out[0])
	}
	// Broken mount degrades to zero, never errors out the whole list.
	if out[1].FreeBytes != 0 || out[1].TotalBytes != 0 {
		t.Errorf("broken mount: %+v, want zero space", out[1])
	}
}
