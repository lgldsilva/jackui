package streamer

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// TestReleaseViewer_NoLeaseIsNoop: a release with no viewer at all (double
// release, client that never acquired) must not report lastViewer — the
// handler would otherwise CloseForHash every user's HLS transcode.
func TestReleaseViewer_NoLeaseIsNoop(t *testing.T) {
	s := leaseTestStreamer()
	h := metainfo.Hash{0x31}
	s.active[h] = &entry{}
	if scheduled, last := s.ReleaseViewer(h); scheduled || last {
		t.Fatalf("release with zero viewers = (%v,%v), want (false,false)", scheduled, last)
	}
}

// TestReleaseViewerFor_OtherOwnerCannotEndLease: user B releasing (or
// double-releasing) must not consume user A's lease.
func TestReleaseViewerFor_OtherOwnerCannotEndLease(t *testing.T) {
	s := leaseTestStreamer()
	h := metainfo.Hash{0x32}
	e := &entry{}
	s.active[h] = e
	s.AcquireViewerFor(h, "u1")

	for i := 0; i < 3; i++ {
		if scheduled, last := s.ReleaseViewerFor(h, "u2"); scheduled || last {
			t.Fatalf("foreign release #%d = (%v,%v), want (false,false)", i, scheduled, last)
		}
	}
	if e.viewers != 1 {
		t.Fatalf("viewers = %d, want 1 (u1 still watching)", e.viewers)
	}
}

func TestReleaseViewerFor_SameOwnerCountsAcquires(t *testing.T) {
	s := leaseTestStreamer()
	h := metainfo.Hash{0x33}
	e := &entry{}
	s.active[h] = e
	s.AcquireViewerFor(h, "u1")
	s.AcquireViewerFor(h, "u1") // two tabs / StrictMode double mount
	s.AcquireViewerFor(h, "u2")

	if _, last := s.ReleaseViewerFor(h, "u1"); last {
		t.Fatal("u1 still has a lease and u2 is watching")
	}
	if _, last := s.ReleaseViewerFor(h, "u2"); last {
		t.Fatal("u1 still has one lease")
	}
	if e.viewers != 1 || e.viewerOwners["u1"] != 1 || len(e.viewerOwners) != 1 {
		t.Fatalf("viewers=%d owners=%v, want 1/{u1:1}", e.viewers, e.viewerOwners)
	}
}

func TestReleaseViewerFor_LastOwnerReportsLast(t *testing.T) {
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
	e := &entry{t: tor}
	s.mu.Lock()
	s.active[h] = e
	s.mu.Unlock()
	s.AcquireViewerFor(h, "u1")

	_, last := s.ReleaseViewerFor(h, "u1")
	s.mu.Lock()
	if e.dropTimer != nil {
		e.dropTimer.Stop()
	}
	s.mu.Unlock()
	if !last {
		t.Fatal("final release must report lastViewer")
	}
}
