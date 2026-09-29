package streamer

import "testing"

// Construct a Streamer with just the fields needed for the protection set —
// avoids spinning up an anacrolix client in a unit test.
func newProtectionTestStreamer() *Streamer {
	return &Streamer{downloads: map[string]struct{}{}}
}

func TestRegisterUnregisterDownload(t *testing.T) {
	s := newProtectionTestStreamer()
	if s.IsDownloadProtected("x") {
		t.Fatal("expected unregistered name to be unprotected")
	}
	s.RegisterDownload("x")
	if !s.IsDownloadProtected("x") {
		t.Fatal("expected registered name to be protected")
	}
	s.UnregisterDownload("x")
	if s.IsDownloadProtected("x") {
		t.Fatal("expected unregistered name to be unprotected again")
	}
	// Idempotent unregister
	s.UnregisterDownload("x")
	// Empty name is a no-op
	s.RegisterDownload("")
	if s.IsDownloadProtected("") {
		t.Fatal("empty name should not be protected")
	}
}

// Regression: anacrolix writes single-file downloads as "<name>.part" while the
// download runs. The worker registers "<name>" (t.Name()), and enforceCacheLimit
// looks up with the on-disk entry ("<name>.part"). Without suffix tolerance the
// LRU deleted the .part and the download started over from zero.
func TestIsDownloadProtectedTolerantesPartSuffix(t *testing.T) {
	s := newProtectionTestStreamer()
	s.RegisterDownload("Star.Wars.mkv")
	if !s.IsDownloadProtected("Star.Wars.mkv.part") {
		t.Fatal("expected .part variant to be protected via TrimSuffix lookup")
	}
	if !s.IsDownloadProtected("Star.Wars.mkv") {
		t.Fatal("expected exact match to still work")
	}
	// File without .part suffix that doesn't match either way stays unprotected.
	if s.IsDownloadProtected("Other.mkv") {
		t.Fatal("unrelated name should not be protected")
	}
	if s.IsDownloadProtected("Other.mkv.part") {
		t.Fatal("unrelated .part should not be protected")
	}
}
