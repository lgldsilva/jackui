//go:build !linux

package local

// detectRemoteFS has no portable statfs equivalent off Linux. JackUI ships and
// runs on Linux (Docker); on other OSes (dev on macOS) we conservatively report
// "not remote" so the cache button stays hidden rather than guessing wrong.
func detectRemoteFS(string) bool { return false }
