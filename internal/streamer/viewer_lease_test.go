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

	if scheduled, last := s.ReleaseViewer(h); scheduled || last {
		t.Fatal("releasing one of two viewers must NOT schedule a drop nor report last-viewer")
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
	if scheduled, last := s.ReleaseViewer(metainfo.Hash{0x09}); scheduled || last {
		t.Fatal("releasing an unknown hash should report not-scheduled and not-last")
	}
}

// TestReleaseViewer_LastViewerKeptTorrentStillReportsLast: a protected background
// download keeps the torrent alive, but the last viewer leaving must still report
// lastViewer=true so the caller tears down the HLS transcode (it only fed the
// player). Regression guard for the idle-CPU fix.
func TestReleaseViewer_LastViewerKeptTorrentStillReportsLast(t *testing.T) {
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	tor, _, err := s.client.AddTorrentSpec(str3TorrentSpec(t))
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	h := tor.InfoHash()
	e := &entry{t: tor, viewers: 1}
	s.mu.Lock()
	s.active[h] = e
	s.downloads[tor.Name()] = struct{}{} // protected background download
	s.mu.Unlock()

	scheduled, last := s.ReleaseViewer(h)
	if scheduled {
		t.Error("protected download must NOT schedule a drop")
	}
	if !last {
		t.Error("last viewer leaving must report lastViewer=true so HLS closes")
	}
	if _, ok := s.active[h]; !ok {
		t.Error("protected torrent must stay active for the download")
	}
}

// TestReleaseViewer_LastViewerStreamOnlySchedulesDrop: the last viewer of a plain
// stream-only torrent both schedules the drop and reports lastViewer=true.
func TestReleaseViewer_LastViewerStreamOnlySchedulesDrop(t *testing.T) {
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	tor, _, err := s.client.AddTorrentSpec(str3TorrentSpec(t))
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	h := tor.InfoHash()
	e := &entry{t: tor, viewers: 1}
	s.mu.Lock()
	s.active[h] = e
	s.mu.Unlock()

	scheduled, last := s.ReleaseViewer(h)
	// Stop the scheduled drop so it can't fire mid-test.
	s.mu.Lock()
	if e.dropTimer != nil {
		e.dropTimer.Stop()
	}
	s.mu.Unlock()
	if !scheduled {
		t.Error("last viewer of a stream-only torrent must schedule a drop")
	}
	if !last {
		t.Error("must report lastViewer=true")
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

func TestDrop_SkipsWhileViewersHold(t *testing.T) {
	s := leaseTestStreamer()
	h := metainfo.Hash{0x05}
	e := &entry{viewers: 1} // a player is watching (t is nil — must not be touched)
	s.active[h] = e

	// A forced Drop (manual StreamDrop / health probe) must be a no-op while a
	// viewer lease is held — and must NOT reach e.t (nil) before bailing.
	s.Drop(h)

	if _, ok := s.active[h]; !ok {
		t.Fatal("Drop must not evict a torrent that still has viewers")
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
