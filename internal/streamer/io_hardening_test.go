package streamer

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// bigNoDataURLazySpec builds an info-complete single-file torrent of `length`
// bytes (16 KiB pieces) whose data is NOT on disk and has no peers — reads
// stall in waitAvailable until their deadline. Piece hashes are placeholders;
// nothing is ever verified without data.
func bigNoDataURLazySpec(t *testing.T, name string, length int64) *torrent.TorrentSpec {
	t.Helper()
	const piece = 1 << 14
	n := (length + piece - 1) / piece
	pieceHash := metainfo.HashBytes([]byte("placeholder"))
	info := metainfo.Info{
		Name:        name,
		PieceLength: piece,
		Length:      length,
		Pieces:      bytes.Repeat(pieceHash[:], int(n)),
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

// goroutineFrames reports how many goroutine stacks mention `needle`.
func goroutineFrames(needle string) int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, needle) {
			count++
		}
	}
	return count
}

// shrinkBudget replaces one of the read budgets for the duration of a test.
func shrinkBudget(t *testing.T, target *time.Duration, to time.Duration) {
	t.Helper()
	old := *target
	*target = to
	t.Cleanup(func() { *target = old })
}

// ─── F8: warmTail's read must end on budget (not leak past Close) ────────────

// TestWarmTail_ReadEndsOnBudget: warmTail fires a detached goroutine whose
// Read blocks in waitAvailable on a stalled swarm. anacrolix Reader.Close()
// does NOT unblock a blocked Read, so the old select-then-Close left that
// goroutine alive (until some unrelated client event woke it — nondeterministic,
// and forever when nothing did). With the reader's context wired to a timer the
// read returns on budget deterministically: warmTail must be fully gone — no
// blocked reader goroutine left behind — shortly after the budget.
func TestWarmTail_ReadEndsOnBudget(t *testing.T) {
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	tor, _, err := s.client.AddTorrentSpec(bigNoDataURLazySpec(t, "warmtail-big.bin", 9<<20)) // > 8 MiB tail
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	files := tor.Files()
	if len(files) == 0 {
		t.Fatal("torrent has no files")
	}
	shrinkBudget(t, &warmTailReadBudget, 300*time.Millisecond)

	start := time.Now()
	s.warmTail(files[0])
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("warmTail blocked for %s — its hint-commit Read ignores Reader.Close and needs the reader's own context", elapsed)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if goroutineFrames("(*Streamer).warmTail.func") == 0 {
			return // read ended on budget; no leaked reader goroutine
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("warmTail's hint-commit read goroutine outlived its budget — leaked blocked in Read (Reader.Close does not unblock it)")
}

// TestPrefetch_ReadGoroutineEndsOnBudget: same leak class as warmTail — after
// the soft deadline the inner Read goroutine stayed blocked in waitAvailable
// (Close doesn't unblock it), one leaked goroutine per prefetch against a
// stalled swarm. The read must end on budget and the goroutine must be gone.
func TestPrefetch_ReadGoroutineEndsOnBudget(t *testing.T) {
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	tor, _, err := s.client.AddTorrentSpec(bigNoDataURLazySpec(t, "prefetch-big.bin", 9<<20))
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	hash := tor.InfoHash()
	s.mu.Lock()
	s.active[hash] = &entry{t: tor, lastAccess: time.Now()}
	s.mu.Unlock()
	shrinkBudget(t, &prefetchReadBudget, 300*time.Millisecond)

	if err := s.Prefetch(hash, 0); err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if goroutineFrames("(*Streamer).Prefetch.func") == 0 {
			return // read ended on budget; goroutine gone
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("Prefetch's hint-commit read goroutine outlived its budget — leaked blocked in Read (Reader.Close does not unblock it)")
}
