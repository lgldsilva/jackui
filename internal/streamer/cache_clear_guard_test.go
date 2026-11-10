package streamer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Regression (#200): ClearAll dropped every active torrent unconditionally —
// killing playback that held a viewer lease — and skipped purgeVerifiedFiles.
func TestClearAll_HonoursViewerLeaseAndPurgesVerifyKeys(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	watched, cleanW := s.SeedActiveForTesting("watched", time.Now().Add(-time.Hour))
	defer cleanW()
	idle, cleanI := s.SeedActiveForTesting("idle", time.Now().Add(-time.Hour))
	defer cleanI()
	s.AcquireViewerFor(watched, "u1:tab")
	s.verifiedFiles[idle.HexString()+"-0"] = true

	if err := s.ClearAll(); err != nil {
		t.Fatalf("ClearAll: %v", err)
	}
	s.mu.Lock()
	_, watchedLive := s.active[watched]
	_, idleLive := s.active[idle]
	s.mu.Unlock()
	if !watchedLive {
		t.Fatal("ClearAll dropped a torrent with a fresh viewer lease")
	}
	if idleLive {
		t.Fatal("ClearAll kept an unwatched torrent")
	}
	if s.verifiedFiles[idle.HexString()+"-0"] {
		t.Fatal("ClearAll left verified-file keys of a dropped torrent")
	}
}

// Regression (#200): "sub/../.metainfo" cleaned to the internal .metainfo dir
// and passed the inside-DataDir guard, deleting the .torrent cache.
func TestClearEntry_RejectsDotEntriesAndWatchedTorrents(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	meta := filepath.Join(s.cfg.DataDir, ".metainfo")
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sub/../.metainfo", ".metainfo", "..", ".", "/etc", "../x"} {
		if err := s.ClearEntry(name); err == nil {
			t.Fatalf("ClearEntry(%q) must be rejected", name)
		}
	}
	if _, err := os.Stat(meta); err != nil {
		t.Fatalf(".metainfo was removed: %v", err)
	}

	h, cleanup := s.SeedActiveForTesting("watched", time.Now().Add(-time.Hour))
	defer cleanup()
	s.AcquireViewerFor(h, "u1:tab")
	dir := filepath.Join(s.cfg.DataDir, "watched")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearEntry("watched"); err == nil {
		t.Fatal("ClearEntry must refuse an entry whose torrent is being watched")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("watched entry bytes were removed: %v", err)
	}

	plain := filepath.Join(s.cfg.DataDir, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearEntry("plain/../plain"); err != nil {
		t.Fatalf("ClearEntry(plain): %v", err)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatalf("plain entry not removed: %v", err)
	}
}
