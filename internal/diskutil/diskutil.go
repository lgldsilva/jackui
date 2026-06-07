// Package diskutil exposes filesystem-level disk usage, shared by the local
// mounts browser and the streaming cache stats so neither has to embed its own
// statfs call (and so the streamer doesn't have to import the handlers package).
package diskutil

import "syscall"

// Usage returns (free, total) bytes for the filesystem holding path.
// Best-effort: returns (0, 0) when statfs fails — e.g. a stale rclone mount or
// a path that no longer exists — so callers just omit the figure instead of
// erroring out.
func Usage(path string) (free, total int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bsize := int64(st.Bsize)
	// Bavail = blocks free for unprivileged users (what's actually usable).
	return bsize * int64(st.Bavail), bsize * int64(st.Blocks)
}
