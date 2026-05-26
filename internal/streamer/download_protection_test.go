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
