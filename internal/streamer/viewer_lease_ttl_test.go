package streamer

import (
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// The frontend releases a viewer lease only on React unmount — there is no
// pagehide/beforeunload release — so a dead tab or mobile app leaves
// viewers > 0 forever. These tests pin the reaper's bound: a lease is trusted
// for ViewerLeaseTTL after the last AcquireViewer, no longer. A torrent pinned
// by a stale lease was unreachable by EVERY teardown path (reaper, drop,
// dropIfStillIdle) — one of the confirmed memory-retention mechanisms.

func TestDropIdleTorrents_DropsStaleViewerLease(t *testing.T) {
	s := NewForTesting()
	s.cfg.IdleTimeout = time.Hour
	s.cfg.ViewerLeaseTTL = 2 * time.Hour

	h := metainfo.Hash{0x10}
	tor := newTestTorrent(t)
	s.mu.Lock()
	s.active[h] = &entry{
		t:          tor,
		lastAccess: time.Now().Add(-2 * time.Hour), // idle beyond IdleTimeout
		viewers:    1,                              // lease never released — client died
		leaseAt:    time.Now().Add(-3 * time.Hour), // acquired once, ages ago
	}
	s.mu.Unlock()

	dropped := s.dropIdleTorrents(time.Now())
	if len(dropped) != 1 || dropped[0] != h {
		t.Fatalf("dropped = %v, want [%v] — a stale viewer lease must not pin the torrent", dropped, h)
	}
}

func TestDropIdleTorrents_KeepsFreshViewerLease(t *testing.T) {
	s := NewForTesting()
	s.cfg.IdleTimeout = time.Hour
	s.cfg.ViewerLeaseTTL = 2 * time.Hour

	h := metainfo.Hash{0x11}
	tor := newTestTorrent(t)
	s.mu.Lock()
	s.active[h] = &entry{
		t:          tor,
		lastAccess: time.Now().Add(-2 * time.Hour), // idle beyond IdleTimeout (paused player)
		viewers:    1,
		leaseAt:    time.Now().Add(-10 * time.Minute), // lease acquired recently
	}
	s.mu.Unlock()

	dropped := s.dropIdleTorrents(time.Now())
	if len(dropped) != 0 {
		t.Fatalf("dropped = %v, want none — a fresh viewer lease still means someone is watching", dropped)
	}
}

// TestBumpAccess_RenewsViewerLease pins the renewal semantics: a real byte
// read with viewers on the torrent renews leaseAt, so a long session (stamped
// once at play start) is not evicted by the reaper's TTL while someone
// actually watches. Reads WITHOUT viewers never manufacture a lease stamp —
// the TTL keeps counting from the last real watch for dead clients.
func TestBumpAccess_RenewsViewerLease(t *testing.T) {
	s := NewForTesting()
	old := time.Now().Add(-3 * time.Hour)

	h := metainfo.Hash{0x21}
	leased := &entry{viewers: 1, leaseAt: old, lastAccess: old}
	s.mu.Lock()
	s.active[h] = leased
	s.mu.Unlock()

	(&trackingReader{streamer: s, hash: h}).bumpAccess()
	if leased.leaseAt.Equal(old) {
		t.Fatal("a real read with viewers must renew the viewer lease")
	}
	if leased.leaseAt.Before(time.Now().Add(-time.Second)) {
		t.Fatal("renewed lease must be stamped ~now")
	}

	h2 := metainfo.Hash{0x22}
	unleased := &entry{viewers: 0, leaseAt: old, lastAccess: old}
	s.mu.Lock()
	s.active[h2] = unleased
	s.mu.Unlock()

	(&trackingReader{streamer: s, hash: h2}).bumpAccess()
	if !unleased.leaseAt.Equal(old) {
		t.Fatal("a read without viewers must not manufacture a lease stamp")
	}
}

// TestDrop_ExpiredLeaseNoLongerBlocks: an explicit stop must not be refused by
// a lease whose client is long dead — the TTL bound applies to the drop guards
// exactly like to the idle reaper.
func TestDrop_ExpiredLeaseNoLongerBlocks(t *testing.T) {
	s := NewForTesting()
	h := metainfo.Hash{0x31}
	tor := newTestTorrent(t)
	s.mu.Lock()
	s.active[h] = &entry{
		t:          tor,
		lastAccess: time.Now().Add(-time.Hour), // beyond activeReadGuard
		viewers:    1,
		leaseAt:    time.Now().Add(-3 * time.Hour), // dead client, lease never released
	}
	s.mu.Unlock()

	s.Drop(h)
	if _, ok := s.active[h]; ok {
		t.Fatal("an expired viewer lease must not block an explicit stop")
	}
}
