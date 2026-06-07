package handlers

import (
	"github.com/luizg/jackui/internal/diskutil"
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

// mountsWithSpace enriches each mount with its filesystem usage.
func mountsWithSpace(mounts []local.Mount) []mountWithSpace {
	out := make([]mountWithSpace, 0, len(mounts))
	for _, m := range mounts {
		free, total := diskutil.Usage(m.Path)
		out = append(out, mountWithSpace{Mount: m, FreeBytes: free, TotalBytes: total})
	}
	return out
}
