package streamer

import (
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// leaseTestStreamer builds a Streamer with just the maps the viewer-lease logic
// touches — no anacrolix client, mirroring the package's other unit tests. The
// final e.t.Drop() path (last viewer leaves a stream-only torrent) needs a real
// torrent and is covered by the manual/integration checks, exactly like gcLoop
// and Drop. These tests pin the counting + scheduling logic that protects
// co-watchers, which is the part that must never regress.
func leaseTestStreamer() *Streamer {
	return &Streamer{
		active:    map[metainfo.Hash]*entry{},
		downloads: map[string]struct{}{},
	}
}

func TestAcquireViewer_IncrementsAndCancelsPendingDrop(t *testing.T) {
	s := leaseTestStreamer()
	h := metainfo.Hash{0x01}
	// Entry with a pending drop timer, as if the last viewer had just left.
	e := &entry{viewers: 0, dropTimer: time.AfterFunc(time.Hour, func() {})}
	s.active[h] = e

	s.AcquireViewer(h)
	if e.viewers != 1 {
		t.Fatalf("viewers = %d, want 1", e.viewers)
	}
	if e.dropTimer != nil {
		t.Fatal("acquire must cancel the pending drop timer")
	}

	s.AcquireViewer(h)
	if e.viewers != 2 {
		t.Fatalf("viewers = %d, want 2", e.viewers)
	}
}

func TestAcquireViewer_UnknownHashNoop(t *testing.T) {
	s := leaseTestStreamer()
	s.AcquireViewer(metainfo.Hash{0x09}) // must not panic on a non-active hash
}

func TestReleaseViewer_KeepsTorrentWhileOtherViewersRemain(t *testing.T) {
	s := leaseTestStreamer()
	h := metainfo.Hash{0x02}
	e := &entry{}
	s.active[h] = e
	s.AcquireViewer(h)
	s.AcquireViewer(h) // viewers = 2 (two browsers watching the same stream)

	if scheduled := s.ReleaseViewer(h); scheduled {
		t.Fatal("releasing one of two viewers must NOT schedule a drop")
	}
	if e.viewers != 1 {
		t.Fatalf("viewers = %d, want 1", e.viewers)
	}
	if e.dropTimer != nil {
		t.Fatal("no drop may be scheduled while a viewer remains")
	}
	if _, ok := s.active[h]; !ok {
		t.Fatal("torrent must stay active while a viewer remains")
	}
}

func TestReleaseViewer_UnknownHashNoop(t *testing.T) {
	s := leaseTestStreamer()
	if s.ReleaseViewer(metainfo.Hash{0x09}) {
		t.Fatal("releasing an unknown hash should report not-scheduled")
	}
}

func TestDropIfStillIdle_SkipsWhenReacquired(t *testing.T) {
	s := leaseTestStreamer()
	h := metainfo.Hash{0x03}
	e := &entry{viewers: 1} // someone re-acquired before the grace timer fired
	s.active[h] = e
	s.dropIfStillIdle(h, e) // viewers > 0 → no-op, must not touch e.t
	if _, ok := s.active[h]; !ok {
		t.Fatal("entry must remain active when a viewer re-acquired")
	}
}

func TestDropIfStillIdle_SkipsWhenEntryReplaced(t *testing.T) {
	s := leaseTestStreamer()
	h := metainfo.Hash{0x04}
	old := &entry{viewers: 0}
	newer := &entry{viewers: 0}
	s.active[h] = newer
	s.dropIfStillIdle(h, old) // stale timer for a replaced entry → no-op
	if s.active[h] != newer {
		t.Fatal("dropIfStillIdle must not disturb a replacement entry")
	}
}
