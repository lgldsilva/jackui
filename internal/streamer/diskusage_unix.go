//go:build unix

package streamer

import (
	"os"
	"syscall"
)

// PhysicalBytes returns the number of bytes actually allocated on disk for the
// file described by info. On POSIX filesystems this is `st.Blocks * 512` (the
// historical stat block unit), which correctly reflects sparse holes — a 10 GB
// sparse file with only one 4 KiB block written returns 4096.
//
// If the underlying syscall struct isn't available for any reason, we fall back
// to the logical size so callers never see zero.
func PhysicalBytes(info os.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return info.Size()
}

// physicalBytes is the legacy lowercase alias for backwards compat within
// the package. Existing call sites use it; new external callers should prefer
// PhysicalBytes.
func physicalBytes(info os.FileInfo) int64 { return PhysicalBytes(info) }
