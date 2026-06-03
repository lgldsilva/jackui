package downloads

import (
	"testing"

	"github.com/luizg/jackui/internal/jackett"
	"github.com/luizg/jackui/internal/streamer"
)

// fakeSearcher is a stub sourceSearcher for rotation tests.
type fakeSearcher struct {
	results []jackett.Result
	calls   int
	err     error
}

func (f *fakeSearcher) Search(_, _ string, _ []string) ([]jackett.Result, error) {
	f.calls++
	return f.results, f.err
}

func newRotationWorker(t *testing.T, store *Store, search sourceSearcher) *Worker {
	t.Helper()
	return NewWorker(WorkerConfig{
		Store:    store,
		Streamer: streamer.NewForTesting(),
		DataDir:  t.TempDir(),
		Jackett:  search,
		Settings: func() QueueSettings {
			return QueueSettings{MaxActive: 3, StallThresholdMin: 30, MaxStalls: 3, RotationEnabled: true}
		},
	})
}

func TestParkCurrentSource_RegistersOriginalThenCooldowns(t *testing.T) {
	store := newTestStore(t)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "orig", FileIndex: 0, Magnet: "magnet:orig", Name: "Orig 2020 1080p", FileSize: 1000})
	w := newRotationWorker(t, store, &fakeSearcher{})

	full, _ := store.Get(1, d.ID)
	w.parkCurrentSource(*full)

	list, _ := store.ListSources(d.ID)
	if len(list) != 1 || list[0].InfoHash != "orig" {
		t.Fatalf("original should be registered, got %+v", list)
	}
	// The active (original) source got marked tried → cooldown.
	if list[0].Status != SourceCooldown || list[0].Tries != 1 {
		t.Errorf("original should be in cooldown after parking, got status=%q tries=%d", list[0].Status, list[0].Tries)
	}
}

func TestDiscoverSources_PersistsMatches(t *testing.T) {
	store := newTestStore(t)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "orig", FileIndex: 0, Magnet: "magnet:orig", Name: "Show S01E01 1080p", FileSize: 1_000_000_000})
	search := &fakeSearcher{results: []jackett.Result{
		{Title: "Show S01E01 1080p WEB", InfoHash: "altA", MagnetURI: "magnet:A", Size: 1_020_000_000, Seeders: 30},
		{Title: "Show S01E09 1080p", InfoHash: "altB", MagnetURI: "magnet:B", Size: 1_000_000_000, Seeders: 99}, // wrong episode
	}}
	w := newRotationWorker(t, store, search)

	full, _ := store.Get(1, d.ID)
	w.discoverSources(*full)

	if search.calls != 1 {
		t.Errorf("expected 1 Jackett search, got %d", search.calls)
	}
	list, _ := store.ListSources(d.ID)
	if len(list) != 1 || list[0].InfoHash != "altA" {
		t.Fatalf("only the matching alternative should persist, got %+v", list)
	}
	if list[0].Status != SourceCandidate {
		t.Errorf("discovered source should be a candidate, got %q", list[0].Status)
	}
}

func TestDiscoverSources_NoJackettIsNoOp(t *testing.T) {
	store := newTestStore(t)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "orig", FileIndex: 0, Magnet: "m", Name: "X", FileSize: 1000})
	w := newRotationWorker(t, store, nil) // no searcher
	full, _ := store.Get(1, d.ID)
	w.discoverSources(*full) // must not panic
	if has, _ := store.HasSources(d.ID); has {
		t.Error("no sources should be discovered without a Jackett client")
	}
}

func TestTryRotate_SwitchesToAlternative(t *testing.T) {
	store := newTestStore(t)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "orig", FileIndex: 0, Magnet: "magnet:orig", Name: "Show S01E01 1080p", FileSize: 1_000_000_000})
	_, _ = store.PromoteToDownloading(d.ID)
	search := &fakeSearcher{results: []jackett.Result{
		{Title: "Show S01E01 1080p REPACK", InfoHash: "altA", MagnetURI: "magnet:A", Size: 1_010_000_000, Seeders: 40},
	}}
	w := newRotationWorker(t, store, search)

	td := &trackedDL{id: d.ID, userID: 1, name: "Show S01E01 1080p", infoHash: "orig"}
	rotated := w.tryRotate(td, w.queueSettings())
	if !rotated {
		t.Fatal("expected rotation to an alternative source")
	}
	// active_magnet now points at the discovered alternative.
	got, _ := store.Get(1, d.ID)
	if got.EffectiveMagnet() != "magnet:A" {
		t.Errorf("expected active magnet magnet:A, got %q", got.EffectiveMagnet())
	}
	// The original is parked (cooldown), the alternative is active.
	list, _ := store.ListSources(d.ID)
	var origStatus, altStatus string
	for _, s := range list {
		if s.InfoHash == "orig" {
			origStatus = s.Status
		}
		if s.InfoHash == "altA" {
			altStatus = s.Status
		}
	}
	if origStatus != SourceCooldown {
		t.Errorf("original should be cooldown, got %q", origStatus)
	}
	if altStatus != SourceActive {
		t.Errorf("alternative should be active, got %q", altStatus)
	}
}

func TestTryRotate_NoAlternativeReturnsFalse(t *testing.T) {
	store := newTestStore(t)
	d, _ := store.Create(Download{UserID: 1, InfoHash: "orig", FileIndex: 0, Magnet: "magnet:orig", Name: "Obscure 2020 1080p", FileSize: 1_000_000_000})
	_, _ = store.PromoteToDownloading(d.ID)
	// Jackett returns nothing usable.
	w := newRotationWorker(t, store, &fakeSearcher{results: nil})

	td := &trackedDL{id: d.ID, userID: 1, name: "Obscure 2020 1080p", infoHash: "orig"}
	if w.tryRotate(td, w.queueSettings()) {
		t.Fatal("expected no rotation when there are no alternatives")
	}
}
