package downloads

import (
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// StopSeedByInfoHash is used by the streaming cards' "remove torrent" action
// (StreamDrop/StreamDropBatch): it must mark the row even when it is paused,
// otherwise the next boot's auto-seed brings the torrent back. The original
// bug: the SQL filtered AND status='completed' and the paused row ended up
// unmarked.
func TestStopSeedByInfoHash_MarksPausedRow(t *testing.T) {
	s := newTestStore(t)
	d, err := s.Create(Download{UserID: 1, InfoHash: testHashHex, Magnet: "magnet:?xt=urn:btih:" + testHashHex, Name: "Movie"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetStatus(1, d.ID, StatusPaused); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if err := s.StopSeedByInfoHash(1, testHashHex); err != nil {
		t.Fatalf("StopSeedByInfoHash: %v", err)
	}
	got, err := s.Get(1, d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SeedStoppedAt == nil {
		t.Error("paused row must be marked seed-stopped so the next boot does not reactivate it")
	}
}

// Worker.Remove must clear the PERSISTED seed (DropSeed semantics, not the
// generic Drop's): without it the next boot's resumeSeeding resurrects the
// trash-removed torrent as a "Seeding" card — exactly streamer.DropSeed's
// comment ("use in explicit actions: stop seeding / remove torrent / delete
// download").
func TestWorkerRemove_ClearsPersistedSeed(t *testing.T) {
	pool := dbtest.NewDB(t)
	seeds, err := streamer.NewSeeds(pool)
	if err != nil {
		t.Fatalf("NewSeeds: %v", err)
	}
	if err := seeds.Add(testHashHex, "magnet:?xt=urn:btih:"+testHashHex, "Movie"); err != nil {
		t.Fatalf("seeds.Add: %v", err)
	}
	s := streamer.NewForTesting()
	s.SetSeeds(seeds)
	w := NewWorker(WorkerConfig{Store: newTestStore(t), Streamer: s, DataDir: t.TempDir(), Interval: time.Hour})

	w.Remove(42, testHashHex)

	if seeds.Has(testHashHex) {
		t.Error("persisted seed must be cleared on Remove so the torrent does not resurrect on next boot")
	}
}
