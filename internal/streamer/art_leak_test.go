package streamer

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// countStuckReaders counts live goroutines whose stack mentions the anacrolix
// reader's waitAvailable — the signature of a reader blocked on a missing
// piece. Counting by stack text is immune to the client's own goroutine churn
// (tracker announcers, piece workers ramping up during the test), which made a
// bare NumGoroutine delta flaky under -race: their baseline drifts by dozens
// while the leak signal is a fixed +1 per call.
func countStuckReaders() int {
	buf := make([]byte, 8<<20)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "waitAvailable")
}

// imageTorrentSpec builds an info-complete torrent holding one poster.jpg big
// enough to pass minTorrentImageBytes. Its pieces never download in the test
// client (offline), so a Read on the file blocks exactly like a stalled swarm.
func imageTorrentSpec(t *testing.T) *torrent.TorrentSpec {
	t.Helper()
	const piece = 32 << 10
	data := bytes.Repeat([]byte("p"), piece) // 32 KiB ≥ minTorrentImageBytes
	pieceHash := metainfo.HashBytes(data)
	info := metainfo.Info{
		Name:        "art-pack",
		PieceLength: piece,
		Files:       []metainfo.FileInfo{{Path: []string{"poster.jpg"}, Length: int64(len(data))}},
		Pieces:      pieceHash[:],
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("bencode.Marshal: %v", err)
	}
	spec, err := torrent.TorrentSpecFromMetaInfoErr(&metainfo.MetaInfo{InfoBytes: infoBytes})
	if err != nil {
		t.Fatalf("TorrentSpecFromMetaInfoErr: %v", err)
	}
	return spec
}

// TestTorrentImage_CanceledContextDoesNotLeakReaders pins the goroutine+reader
// leak fix in TorrentImage: with the caller's context wired into the reader
// (SetContext), a canceled request ends the read and the reader closes with it.
// Before the fix the read ran in a detached goroutine that Close could not
// unblock — one leaked goroutine+reader per timed-out card on a stalled swarm
// (same class Prefetch/warmTail already fixed; the repo documents that
// Reader.Close() does NOT unblock a Read blocked in waitAvailable).
func TestTorrentImage_CanceledContextDoesNotLeakReaders(t *testing.T) {
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	tor, _, err := s.client.AddTorrentSpec(imageTorrentSpec(t))
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	h := tor.InfoHash()
	s.mu.Lock()
	s.active[h] = &entry{t: tor, lastAccess: time.Now()}
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Amortized over enough calls that the signal dominates: pre-fix every call
	// leaves ONE reader goroutine blocked in waitAvailable FOREVER (+calls);
	// post-fix the read ends with the canceled context and the count returns to
	// baseline regardless of unrelated client churn.
	before := countStuckReaders()
	const calls = 20
	for i := 0; i < calls; i++ {
		if _, _, err := s.TorrentImage(ctx, h); err == nil {
			t.Fatal("expected a context error from a canceled TorrentImage")
		}
	}
	time.Sleep(250 * time.Millisecond)
	if after := countStuckReaders(); after > before+2 {
		t.Fatalf("waitAvailable-blocked readers grew %d → %d across %d canceled TorrentImage calls — reads leak goroutine+reader",
			before, after, calls)
	}
}
