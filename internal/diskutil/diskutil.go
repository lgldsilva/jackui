// Package diskutil exposes filesystem-level disk usage, shared by the local
// mounts browser and the streaming cache stats so neither has to embed its own
// statfs call (and so the streamer doesn't have to import the handlers package).
package diskutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

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
	// #nosec G115 -- conversao limitada (statfs/tempo Unix/id/rune ASCII/fs magic); sem overflow real
	return bsize * int64(st.Bavail), bsize * int64(st.Blocks)
}

// IsRotational reports whether the block device backing path is a spinning HDD
// (rotational=1 in sysfs). Callers use it to avoid running parallel copies on
// the same HDD: concurrent reads/writes make the head seek between them and
// throughput collapses versus a single sequential copy.
//
// Linux-only and best-effort: returns false on ANY failure (FUSE/rclone,
// overlay, unknown device, non-Linux) — i.e. "treat as non-rotational, allow
// parallelism" — so it never blocks a transfer, only opts into serialization
// when it's sure the backing device spins.
func IsRotational(path string) bool {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return false
	}
	maj, min := unix.Major(uint64(st.Dev)), unix.Minor(uint64(st.Dev))
	// /sys/dev/block/MAJ:MIN symlinks to …/block/<disk>[/<part>].
	link, err := os.Readlink(fmt.Sprintf("/sys/dev/block/%d:%d", maj, min))
	if err != nil {
		return false
	}
	dev := filepath.Base(link) // e.g. "sdc1", "sdc", "nvme0n1p2"
	// queue/rotational lives on the whole-disk node; a partition has none, so
	// fall back to its parent disk.
	for _, name := range []string{dev, parentDisk(dev)} {
		if name == "" {
			continue
		}
		// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
		b, err := os.ReadFile("/sys/block/" + name + "/queue/rotational")
		if err == nil {
			return strings.TrimSpace(string(b)) == "1"
		}
	}
	return false
}

// parentDisk strips a partition suffix to get the whole-disk name:
// "sdc1"→"sdc", "nvme0n1p2"→"nvme0n1", "mmcblk0p1"→"mmcblk0". Returns "" when
// the name is already a whole disk (no trailing partition number).
func parentDisk(part string) string {
	// nvme/mmc partitions are "<disk>p<N>"; strip from the "p".
	if strings.HasPrefix(part, "nvme") || strings.HasPrefix(part, "mmcblk") {
		if i := strings.LastIndex(part, "p"); i > 0 && allDigits(part[i+1:]) {
			return part[:i]
		}
		return ""
	}
	// sd*/vd*/hd*: strip trailing digits.
	i := len(part)
	for i > 0 && part[i-1] >= '0' && part[i-1] <= '9' {
		i--
	}
	if i == len(part) {
		return "" // no trailing digits → already a whole disk
	}
	return part[:i]
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
