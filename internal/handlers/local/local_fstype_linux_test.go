//go:build linux

package local

import (
	"os"
	"testing"
)

func TestIsRemoteMagic(t *testing.T) {
	remote := []uint32{fuseSuperMagic, nfsSuperMagic, cifsMagic, smb2Magic}
	for _, m := range remote {
		if !isRemoteMagic(m) {
			t.Errorf("magic %#x: got false, want true (remote)", m)
		}
	}
	local := []uint32{
		0xEF53,     // ext2/3/4
		0x58465342, // xfs
		0x9123683E, // btrfs
		0x01021994, // tmpfs
		0,
	}
	for _, m := range local {
		if isRemoteMagic(m) {
			t.Errorf("magic %#x: got true, want false (local)", m)
		}
	}
}

func TestDetectRemoteFS_LocalDirIsNotRemote(t *testing.T) {
	// A temp dir lives on local disk/tmpfs — never classified as remote.
	if detectRemoteFS(t.TempDir()) {
		t.Fatal("temp dir reported as remote")
	}
	// A bogus path → statfs error → false (treated as local).
	if detectRemoteFS(os.DevNull + "/nope") {
		t.Fatal("unstatable path should be false")
	}
}
