package streamer

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// ─── Regression for the 2026-09-28 incident ("Stop" did nothing) ────────────
//
// In production, the *arr stack's torrent-get poll (~every 60s) refreshed the
// lastAccess of every active torrent via Get(); the 60s activeReadGuard on
// drop stayed permanently armed and every "Stop" was silently refused
// (6× DELETE → 6× 200, torrent alive). The fix: monitoring reads (GetUntouched)
// do not count as use, and the EXPLICIT path (DropSeed) bypasses the read
// guard — refusing only on viewer lease / active background download.

// Get() still counts as use (a real read: player, info resolution).
func TestGet_BumpsLastAccess(t *testing.T) {
	s := NewForTesting()
	h, cleanup := s.SeedActiveForTesting("guard-bump", time.Now().Add(-2*time.Hour))
	defer cleanup()

	s.mu.Lock()
	before := s.active[h].lastAccess
	s.mu.Unlock()
	if time.Since(before) < time.Hour {
		t.Fatalf("setup: entry should be stale (lastAccess=%s)", before)
	}

	if _, err := s.Get(h); err != nil {
		t.Fatalf("Get: %v", err)
	}

	s.mu.Lock()
	after := s.active[h].lastAccess
	s.mu.Unlock()
	if time.Since(after) > time.Minute {
		t.Fatalf("Get did not bump lastAccess: before=%s after=%s", before, after)
	}
}

// GetUntouched() is the MONITORING path: same snapshot, no bump.
func TestGetUntouched_DoesNotBumpLastAccess(t *testing.T) {
	s := NewForTesting()
	stale := time.Now().Add(-2 * time.Hour)
	h, cleanup := s.SeedActiveForTesting("monitor-poll", stale)
	defer cleanup()

	info, err := s.GetUntouched(h)
	if err != nil {
		t.Fatalf("GetUntouched: %v", err)
	}
	if info == nil || info.Name != "monitor-poll" {
		t.Fatalf("GetUntouched returned an invalid snapshot: %+v", info)
	}

	s.mu.Lock()
	after := s.active[h].lastAccess
	s.mu.Unlock()
	if !after.Equal(stale) {
		t.Fatalf("GetUntouched refreshed lastAccess: %s (want %s)", after, stale)
	}
}

// The GENERIC drop path (idle reaper, health probe, lifecycle teardown) stays
// guarded by activeReadGuard: silent refusal on a recent read. The *arr poll
// now uses GetUntouched, but a REAL read (player/HLS) must still block it.
func TestDrop_GenericPath_StillGuarded_AfterRecentRead(t *testing.T) {
	s := NewForTesting()
	h, cleanup := s.SeedActiveForTesting("lifecycle-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

	if _, err := s.Get(h); err != nil { // real read
		t.Fatalf("Get: %v", err)
	}

	s.Drop(h) // intentionally silent refusal on this path

	s.mu.Lock()
	_, stillActive := s.active[h]
	s.mu.Unlock()
	if !stillActive {
		t.Fatal("generic Drop should have been refused (real read < 60s)")
	}
}

// FIX for the incident: "Stop" (explicit path, DropSeed) works even seconds
// after a monitoring Get — the read guard does not apply to a user action.
func TestDropSeed_BypassesReadGuard_AfterMonitoringGet(t *testing.T) {
	s := NewForTesting()
	h, cleanup := s.SeedActiveForTesting("incident-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

	if _, err := s.Get(h); err != nil { // this is what armed the guard in the incident
		t.Fatalf("Get (*arr poll): %v", err)
	}

	if err := s.DropSeed(h); err != nil {
		t.Fatalf("DropSeed after monitoring poll: %v (explicit action must beat the read guard)", err)
	}

	s.mu.Lock()
	_, stillActive := s.active[h]
	s.mu.Unlock()
	if stillActive {
		t.Fatal("DropSeed should have removed the torrent even with Get < 60s ago")
	}
}

// What explicit does NOT beat: an active viewer lease (someone watching).
func TestDropSeed_StillRefused_WhileViewerLeaseActive(t *testing.T) {
	s := NewForTesting()
	h, cleanup := s.SeedActiveForTesting("being-watched", time.Now().Add(-2*time.Hour))
	defer cleanup()

	s.AcquireViewer(h)
	err := s.DropSeed(h)
	if !errors.Is(err, ErrTorrentViewerActive) {
		t.Fatalf("DropSeed = %v, want ErrTorrentViewerActive", err)
	}
	s.mu.Lock()
	_, stillActive := s.active[h]
	s.mu.Unlock()
	if !stillActive {
		t.Fatal("torrent with a viewer lease must NOT be dropped")
	}

	// Last viewer leaves → "Stop" works now.
	s.ReleaseViewer(h)
	if err := s.DropSeed(h); err != nil {
		t.Fatalf("DropSeed after ReleaseViewer: %v", err)
	}
}

// ...and a registered background download (downloads worker protection).
func TestDropSeed_StillRefused_WhileBackgroundDownloadActive(t *testing.T) {
	s := NewForTesting()
	h, cleanup := s.SeedActiveForTesting("background-dl", time.Now().Add(-2*time.Hour))
	defer cleanup()

	name := "background-dl"
	s.RegisterDownload(name)
	if err := s.DropSeed(h); !errors.Is(err, ErrTorrentDownloadProtected) {
		t.Fatalf("DropSeed = %v, want ErrTorrentDownloadProtected", err)
	}

	s.UnregisterDownload(name)
	if err := s.DropSeed(h); err != nil {
		t.Fatalf("DropSeed after UnregisterDownload: %v", err)
	}
}

// DropSeed on an unknown hash is idempotent and honest: ErrTorrentNotActive.
func TestDropSeed_IdempotentOnUnknownHash(t *testing.T) {
	s := NewForTesting()
	if err := s.DropSeed(metainfo.Hash{0xAA}); !errors.Is(err, ErrTorrentNotActive) {
		t.Fatalf("DropSeed on unknown hash = %v, want ErrTorrentNotActive", err)
	}
}

// Generic Drop on an unknown hash stays a silent no-op.
func TestDrop_NoopOnUnknownHash(t *testing.T) {
	s := NewForTesting()
	s.Drop(metainfo.Hash{0xBB}) // must not panic
}

// Control: generic drop on an idle torrent (> 60s) removes it — the read
// guard only protects the recent window.
func TestDrop_Succeeds_WhenIdleBeyondGuard(t *testing.T) {
	s := NewForTesting()
	h, cleanup := s.SeedActiveForTesting("idle-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

	s.Drop(h)

	s.mu.Lock()
	_, stillActive := s.active[h]
	s.mu.Unlock()
	if stillActive {
		t.Fatal("Drop on an idle torrent (> 60s) should remove it")
	}
}

// GetUntouched on a hash that already left the active set returns the same
// error as Get — the *arr poll must be able to tell "gone" from a valid
// snapshot.
func TestGetUntouched_UnknownHash(t *testing.T) {
	s := NewForTesting()
	info, err := s.GetUntouched(metainfo.Hash{0xCC})
	if !errors.Is(err, errTorrentGone) {
		t.Fatalf("GetUntouched on unknown hash = (%v, %v), want errTorrentGone", info, err)
	}
	if info != nil {
		t.Fatalf("GetUntouched on unknown hash returned a snapshot: %+v", info)
	}
	if _, err := s.Get(metainfo.Hash{0xCC}); !errors.Is(err, errTorrentGone) {
		t.Fatalf("Get on unknown hash = %v, want errTorrentGone (same contract as GetUntouched)", err)
	}
}

// IsDropRefusal is the contract handlers use to choose 409 vs 200: only a
// viewer lease or a background download is a refusal; ErrTorrentNotActive is
// idempotent success and generic errors (even wrapped) are not refusals.
func TestIsDropRefusal_Classification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"viewer lease", ErrTorrentViewerActive, true},
		{"protected download", ErrTorrentDownloadProtected, true},
		{"wrapped viewer lease", fmt.Errorf("drop %s: %w", "abc", ErrTorrentViewerActive), true},
		{"not active = idempotent", ErrTorrentNotActive, false},
		{"recent read (internal guard)", errRecentlyRead, false},
		{"generic", errors.New("any error"), false},
	}
	for _, tc := range cases {
		if got := IsDropRefusal(tc.err); got != tc.want {
			t.Errorf("IsDropRefusal(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// mustFixture is the single failure point of the SeedActiveForTesting
// fixture: nil passes silently; an error panics with the failing step named,
// so the test using the fixture dies at the right place instead of at some
// downstream assertion.
func TestMustFixture(t *testing.T) {
	mustFixture("noop", nil)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("mustFixture with an error should panic")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "SeedActiveForTesting: step-x: boom") {
			t.Fatalf("panic = %v, want step + cause in the message", r)
		}
	}()
	mustFixture("step-x", errors.New("boom"))
}
