package streamer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// ─── F1: dropIfStillIdle must honor activeReadGuard ──────────────────────────

// TestStreamer_DropIfStillIdle_RespectsActiveReadGuard: the viewer-grace drop
// checked identity, viewers and download protection — but NOT the 60s
// activeReadGuard. A co-watcher mid-playback WITHOUT a viewer lease (lease
// acquisition is best-effort across restarts) had its torrent dropped 8s after
// ANOTHER viewer closed, killing the survivor's playback. A recent
// lastAccess (trackingReader bumps it on every read) must refuse the drop.
func TestStreamer_DropIfStillIdle_RespectsActiveReadGuard(t *testing.T) {
	s := NewForTesting()
	h := metainfo.Hash{0x11}
	tor := newTestTorrent(t)
	e := &entry{t: tor, lastAccess: time.Now()} // co-watcher read moments ago
	s.mu.Lock()
	s.active[h] = e
	s.mu.Unlock()

	// Simulates the viewer-grace timer firing (dropIfStillIdle IS the timer
	// callback) while an unleased co-watcher is still mid-playback.
	s.dropIfStillIdle(h, e)

	s.mu.Lock()
	_, stillActive := s.active[h]
	s.mu.Unlock()
	if !stillActive {
		t.Fatal("grace fire dropped a torrent read within activeReadGuard — co-watcher playback killed")
	}

	// Once the read-guard window passes, the same grace fire must drop it.
	s.mu.Lock()
	e.lastAccess = time.Now().Add(-2 * activeReadGuard)
	s.mu.Unlock()
	s.dropIfStillIdle(h, e)
	s.mu.Lock()
	_, stillActive = s.active[h]
	s.mu.Unlock()
	if stillActive {
		t.Fatal("stale torrent survived dropIfStillIdle past the read guard")
	}
}

// ─── F2: dropIdleTorrents must honor viewer leases ───────────────────────────

// TestStreamer_DropIdleTorrents_KeepsViewerLeased: a leased player paused or
// buffering longer than IdleTimeout got dropped by the idle reaper; the stale
// viewer-close afterwards killed a NEW entry when another viewer started fresh
// playback. e.viewers > 0 must win over idleness, exactly like Drop() does.
func TestStreamer_DropIdleTorrents_KeepsViewerLeased(t *testing.T) {
	s := NewForTesting()
	s.cfg.IdleTimeout = time.Hour

	leasedH := metainfo.Hash{0x21}
	idleH := metainfo.Hash{0x22}
	leased := &entry{t: newTestTorrent(t), lastAccess: time.Now().Add(-2 * time.Hour), viewers: 1}
	idle := &entry{t: newTestTorrent(t), lastAccess: time.Now().Add(-2 * time.Hour)}
	s.mu.Lock()
	s.active[leasedH] = leased
	s.active[idleH] = idle
	s.mu.Unlock()

	dropped := s.dropIdleTorrents(time.Now())
	if len(dropped) != 1 || dropped[0] != idleH {
		t.Fatalf("dropped = %v, want only %v (viewer-leased entry must survive IdleTimeout)", dropped, idleH)
	}
	s.mu.Lock()
	_, leaseStillActive := s.active[leasedH]
	s.mu.Unlock()
	if !leaseStillActive {
		t.Fatal("paused/buffering leased player idle beyond IdleTimeout must NOT be dropped")
	}
}

// ─── F3: ClearEntry must refuse the DataDir itself ───────────────────────────

// TestClearEntry_RefusesDataDirItself: the prefix guard permitted abs == dirAbs,
// so DELETE /api/stream/cache?entry=. wiped the ENTIRE DataDir — favorites
// bytes, .metainfo, .piece-completion-dl included. "." / ".." / "" must be
// rejected without touching anything; a real entry must still clear.
func TestClearEntry_RefusesDataDirItself(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "real-entry")
	if err := os.MkdirAll(marker, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(marker, "data.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(dir, ".metainfo")
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meta, "a.torrent"), []byte("d4:infod0:e"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := leaseTestStreamer()
	s.cfg.DataDir = dir

	for _, name := range []string{".", "..", ""} {
		if err := s.ClearEntry(name); err == nil {
			t.Errorf("ClearEntry(%q) must be refused", name)
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("DataDir content wiped by a refused/ambiguous ClearEntry: %v", err)
	}
	if _, err := os.Stat(meta); err != nil {
		t.Fatalf(".metainfo wiped by a refused/ambiguous ClearEntry: %v", err)
	}

	if err := s.ClearEntry("real-entry"); err != nil {
		t.Fatalf("ClearEntry(real-entry): %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("real-entry should be gone after ClearEntry, stat err = %v", err)
	}
}

// ─── F5: ClearAll must keep download-protected entries ───────────────────────

// TestClearAll_KeepsDownloadProtectedEntries: in legacy storage mode a
// completed download's bytes live INSIDE DataDir. "Clear cache" deleted them
// while the downloads DB row still said completed. Entries whose name is in the
// downloads protection registry must survive ClearAll.
func TestClearAll_KeepsDownloadProtectedEntries(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"dl-entry", "plain-entry"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s := leaseTestStreamer()
	s.cfg.DataDir = dir
	s.RegisterDownload("dl-entry")

	if err := s.ClearAll(); err != nil {
		t.Fatalf("ClearAll: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "dl-entry")); err != nil {
		t.Errorf("download-protected entry must survive ClearAll: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plain-entry")); !os.IsNotExist(err) {
		t.Errorf("plain entry should be removed by ClearAll, stat err = %v", err)
	}
}

// ─── F10: Close must be idempotent ───────────────────────────────────────────

// TestStreamer_Close_Idempotent: Close() closed s.stop unconditionally, so a
// second call (manual shutdown racing the GC loop's teardown, or a defensive
// caller) panicked with "close of closed channel".
func TestStreamer_Close_Idempotent(t *testing.T) {
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Close()
	s.Close() // must be a no-op, not a panic
}
