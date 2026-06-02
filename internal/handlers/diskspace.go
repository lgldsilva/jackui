package handlers

import (
	"syscall"

	"github.com/luizg/jackui/internal/local"
)

// mountWithSpace is a Mount enriched with the free/total bytes of the
// filesystem that hosts it — so the UI can show how much space is available
// per directory (physical disks, rclone mounts, etc).
type mountWithSpace struct {
	local.Mount
	FreeBytes  int64 `json:"freeBytes"`
	TotalBytes int64 `json:"totalBytes"`
}

// diskUsage returns (free, total) bytes for the filesystem holding path.
// Best-effort: returns (0, 0) when statfs fails — e.g. a stale rclone mount or
// a path that no longer exists — so the UI just omits the figure instead of
// erroring out for one bad mount.
func diskUsage(path string) (free, total int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bsize := int64(st.Bsize)
	// Bavail = blocks free for unprivileged users (what's actually usable).
	return bsize * int64(st.Bavail), bsize * int64(st.Blocks)
}

// mountsWithSpace enriches each mount with its filesystem usage.
func mountsWithSpace(mounts []local.Mount) []mountWithSpace {
	out := make([]mountWithSpace, 0, len(mounts))
	for _, m := range mounts {
		free, total := diskUsage(m.Path)
		out = append(out, mountWithSpace{Mount: m, FreeBytes: free, TotalBytes: total})
	}
	return out
}
