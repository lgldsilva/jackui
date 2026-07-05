//go:build linux

package local

import "syscall"

// Filesystem magics for storage that lives off the local block device — i.e.
// slow/remote mounts worth caching to disk. rclone (Google Drive, etc.) mounts
// via FUSE; NAS shares come in over NFS or CIFS/SMB.
const (
	fuseSuperMagic = 0x65735546 // rclone & every other FUSE backend
	nfsSuperMagic  = 0x6969
	cifsMagic      = 0xFF534D42
	smb2Magic      = 0xFE534D42
)

// detectRemoteFS reports whether the filesystem backing abs is a remote/FUSE
// mount (rclone, NFS, CIFS/SMB). A plain local disk (ext4/xfs/btrfs/...) returns
// false — files there are already fast and seekable, so there's nothing to cache.
// On any statfs error we return false (treat as local; don't offer a pointless
// cache action).
func detectRemoteFS(abs string) bool {
	var st syscall.Statfs_t
	if err := syscall.Statfs(abs, &st); err != nil {
		return false
	}
	// #nosec G115 -- conversao limitada (statfs/tempo Unix/id/rune ASCII/fs magic); sem overflow real
	return isRemoteMagic(uint32(st.Type))
}

// isRemoteMagic maps a statfs f_type magic to "remote storage worth caching".
// Pure (no syscalls) so the classification is unit-testable without a real
// FUSE/NAS mount.
func isRemoteMagic(magic uint32) bool {
	switch magic {
	case fuseSuperMagic, nfsSuperMagic, cifsMagic, smb2Magic:
		return true
	default:
		return false
	}
}
