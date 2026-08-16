package downloads

import (
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// StopSeedByInfoHash é usado pelo "remover torrent" dos cards de streaming
// (StreamDrop/StreamDropBatch): precisa marcar a row mesmo quando ela está
// pausada, senão o auto-seed do próximo boot traz o torrent de volta. O bug
// original: o SQL filtrava AND status='completed' e a row pausada ficava sem a
// marcação.
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

// Worker.Remove precisa limpar o seed PERSISTIDO (semântica DropSeed, não a do
// Drop genérico): sem isso o resumeSeeding do próximo boot ressuscita o torrent
// removido pela lixeira como card "Semeando" — exatamente o comentário de
// streamer.DropSeed ("usar nas ações explícitas: parar de seedar / remover
// torrent / excluir download").
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
