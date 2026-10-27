package streamer

import (
	"context"
	"testing"
	"time"
)

// deadMagnet is a syntactically valid magnet whose metadata can never arrive:
// the infohash is a dummy and the test client has no useful peers.
const deadMagnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=dead-magnet"

// TestAddMetadataTimeoutDropsOrphanTorrent pins the orphan-torrent retention
// fix. resolveSource adds the torrent to the anacrolix client (and injects the
// public trackers) BEFORE waitForMetadata; when the wait gives up,
// addWithStorage used to return without registering the torrent and without
// dropping it — it stayed in the client forever, invisible to every reaper
// (they only walk s.active), still announcing and — if metadata arrived late —
// holding make([]Piece, NumPieces) (288 B/piece on v1.61.0) for the life of
// the process.
func TestAddMetadataTimeoutDropsOrphanTorrent(t *testing.T) {
	s, err := newTestStreamer(t, Config{
		DataDir:      t.TempDir(),
		MetadataWait: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	if _, err := s.Add(context.Background(), deadMagnet); err == nil {
		t.Fatal("expected the metadata wait to fail for an unreachable magnet")
	}

	if got := len(s.client.Torrents()); got != 0 {
		t.Fatalf("client still holds %d orphan torrent(s) after a failed Add — retention leak", got)
	}
}

// TestAddCanceledContextDropsOrphanTorrent: a caller giving up (ctx canceled
// while the metadata wait is still running) must leave no orphan behind either.
func TestAddCanceledContextDropsOrphanTorrent(t *testing.T) {
	s, err := newTestStreamer(t, Config{
		DataDir:      t.TempDir(),
		MetadataWait: time.Minute, // long: the canceled ctx must be what ends the wait
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := s.Add(ctx, deadMagnet); err == nil {
		t.Fatal("expected the canceled context to fail the Add")
	}

	if got := len(s.client.Torrents()); got != 0 {
		t.Fatalf("client still holds %d orphan torrent(s) after a canceled Add — retention leak", got)
	}
}

// TestWaitForMetadata_InfoPresentWinsOverDeadline pins the final arbiter: when
// the deadline has already fired but the metadata IS there (it landed exactly
// at the deadline, or a concurrent Add of the same magnet fetched it), the
// wait must SUCCEED — never error into the Drop() path over a handle another
// Add just registered.
func TestWaitForMetadata_InfoPresentWinsOverDeadline(t *testing.T) {
	tor := newTestTorrent(t) // info-complete
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // deadline already fired
	if err := waitForMetadata(ctx, tor, time.Second); err != nil {
		t.Fatalf("metadata present must win over an expired deadline: %v", err)
	}
}

// TestWatchOrphans_StrikesOnClientActiveGap drives the retention watchdog: a
// handle in the client but not in s.active accrues strikes (the warning fires
// at 2 consecutive ticks — longer than any legitimate metadata wait);
// registering the handle closes the gap and resets the count.
func TestWatchOrphans_StrikesOnClientActiveGap(t *testing.T) {
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	tor, _, err := s.client.AddTorrentSpec(str3TorrentSpec(t)) // in client, NOT active
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}

	s.watchOrphans()
	if s.orphanStrikes != 1 {
		t.Fatalf("strikes = %d, want 1 after the first gap tick", s.orphanStrikes)
	}
	s.watchOrphans()
	if s.orphanStrikes != 2 {
		t.Fatalf("strikes = %d, want 2 after the second gap tick", s.orphanStrikes)
	}

	s.mu.Lock()
	s.active[tor.InfoHash()] = &entry{t: tor, lastAccess: time.Now()}
	s.mu.Unlock()
	s.watchOrphans()
	if s.orphanStrikes != 0 {
		t.Fatalf("strikes = %d, want 0 once every handle is registered", s.orphanStrikes)
	}
}
