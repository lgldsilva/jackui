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

// Regression: anacrolix grava single-file como "<name>.part" durante o
// download. O worker registra "<name>" (t.Name()), e o enforceCacheLimit
// consulta com a entry do disco ("<name>.part"). Sem tolerância ao sufixo
// o LRU deletava o .part e o download recomeçava do zero.
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
