package downloads

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// dropCompletedNonSeedHandles must Drop every COMPLETED row that is not a
// seed-tracker torrent, so a post-upgrade boot frees mmap/RSS left by the
// old "keep open after bulk finalize" behaviour.
func TestDropCompletedNonSeedHandles(t *testing.T) {
	store := newTestStore(t)
	hashHex := "aabbccddeeff00112233445566778899aabbccdd"
	d, err := store.Create(Download{
		UserID: 1, InfoHash: hashHex, FileIndex: 0, FilePath: "x.mkv",
		FileSize: 1, Name: "x", Magnet: "magnet:?xt=urn:btih:" + hashHex,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus(1, d.ID, StatusCompleted); err != nil {
		t.Fatal(err)
	}

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: streamer.NewForTesting(),
		DataDir:  t.TempDir(),
	})
	var drops []metainfo.Hash
	w.drop = func(h metainfo.Hash) { drops = append(drops, h) }

	w.dropCompletedNonSeedHandles()

	if len(drops) != 1 {
		t.Fatalf("drops=%d want 1", len(drops))
	}
	if drops[0].HexString() != hashHex {
		t.Fatalf("dropped hash %s want %s", drops[0].HexString(), hashHex)
	}
}
