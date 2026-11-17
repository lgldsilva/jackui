package streamer

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/transcode"
)

// The HLS loopback server discovers the per-request cursor through this
// interface: a signature drift would silently fall back to the locked cursor.
var _ transcode.SourceForker = (*trackingReader)(nil)

const forkPieceLen = 1 << 14

func forkFixture(t *testing.T) (*Streamer, *trackingReader) {
	t.Helper()
	s, hash := activeMultiPiece(t, make([]byte, 40*forkPieceLen), forkPieceLen)
	r, _, err := s.FileReader(hash, 0)
	if err != nil {
		t.Fatalf("FileReader: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	tr := r.(*trackingReader)
	// A window smaller than the file, so a cursor's Now range is a real subset.
	tr.window = 4 * forkPieceLen
	tr.last = pieceWindow{}
	tr.applyWindow(0)
	return s, tr
}

func TestForkSource_IndependentCursorReleasesItsWindow(t *testing.T) {
	_, parent := forkFixture(t)
	if parent.last.empty() {
		t.Fatal("fixture: parent should start with a head window")
	}

	rc, err := parent.ForkSource(context.Background())
	if err != nil {
		t.Fatalf("ForkSource: %v", err)
	}
	fork, ok := rc.(*forkedReader)
	if !ok {
		t.Fatalf("got %T, want *forkedReader", rc)
	}
	if fork.Reader == parent.Reader || fork.file != parent.file || fork.window != parent.window {
		t.Fatal("fork must be a new reader over the same file with the same window")
	}
	if !parent.last.empty() {
		t.Fatalf("parent window not parked: %+v", parent.last)
	}

	if _, err := fork.Seek(20*forkPieceLen, io.SeekStart); err != nil {
		t.Fatalf("fork Seek: %v", err)
	}
	if fork.last.nowBegin != 20 || fork.last.nowEnd != 24 {
		t.Fatalf("fork window = %+v, want Now over pieces [20,24)", fork.last)
	}
	if parent.pos != 0 {
		t.Fatalf("fork moved the parent cursor to %d", parent.pos)
	}

	if err := fork.Close(); err != nil {
		t.Fatalf("fork Close: %v", err)
	}
	if !fork.last.empty() {
		t.Fatalf("closed fork kept its window: %+v", fork.last)
	}
	fork.releaseWindow() // idempotent on an already-released window

	// Parking is one-shot: a second fork works and leaves the parent alone.
	second, err := parent.ForkSource(context.Background())
	if err != nil {
		t.Fatalf("second ForkSource: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestForkSource_AbandonsBlockedReadWhenContextEnds(t *testing.T) {
	_, parent := forkFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	fork, err := parent.ForkSource(ctx)
	if err != nil {
		t.Fatalf("ForkSource: %v", err)
	}
	t.Cleanup(func() { _ = fork.Close() })

	// Nothing is on disk and there are no peers: the read parks in anacrolix
	// until the context ends.
	done := make(chan error, 1)
	go func() {
		_, rerr := fork.Read(make([]byte, 1024))
		done <- rerr
	}()
	cancel()
	select {
	case rerr := <-done:
		if rerr == nil {
			t.Fatal("read of missing data returned without error")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("blocked read survived its context")
	}
}

func TestForkSource_RefusesInactiveTorrent(t *testing.T) {
	s, parent := forkFixture(t)

	if _, err := (&trackingReader{streamer: s, hash: parent.hash}).ForkSource(context.Background()); !errors.Is(err, ErrTorrentNotActive) {
		t.Fatalf("no file: err = %v, want ErrTorrentNotActive", err)
	}

	s.mu.Lock()
	e := s.active[parent.hash]
	delete(s.active, parent.hash)
	s.mu.Unlock()
	if _, err := parent.ForkSource(context.Background()); !errors.Is(err, ErrTorrentNotActive) {
		t.Fatalf("dropped torrent: err = %v, want ErrTorrentNotActive", err)
	}

	// Re-added under the same hash: the old file handle belongs to a dead torrent.
	s.mu.Lock()
	s.active[parent.hash] = &entry{t: nil, lastAccess: e.lastAccess}
	s.mu.Unlock()
	if _, err := parent.ForkSource(context.Background()); !errors.Is(err, ErrTorrentNotActive) {
		t.Fatalf("replaced torrent: err = %v, want ErrTorrentNotActive", err)
	}
	s.mu.Lock()
	s.active[parent.hash] = e // let Close tear the real torrent down
	s.mu.Unlock()
}
