//go:build !unix

package streamer

import "os"

// Fallback for non-Unix platforms (Windows). Logical size is the best signal we
// have without platform-specific code, and Windows NTFS sparse-file metadata
// isn't reachable through os.FileInfo. JackUI ships on Linux containers so this
// is a defensive stub; revisit if a Windows target is ever supported.
func physicalBytes(info os.FileInfo) int64 {
	return info.Size()
}
