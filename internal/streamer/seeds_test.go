package streamer

import (
	"path/filepath"

	"github.com/anacrolix/torrent/metainfo"
	"testing"
)

func newSeedsForTest(t *testing.T) *SeedsStore {
	t.Helper()
	s, err := NewSeeds(filepath.Join(t.TempDir(), "seeds.db"))
	if err != nil {
		t.Fatalf("NewSeeds: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSeedsStoreAddListHasRemove(t *testing.T) {
	s := newSeedsForTest(t)

	if s.Has("abc") {
		t.Fatal("Has should be false on empty store")
	}
	if err := s.Add("abc", "magnet:?xt=urn:btih:abc", "Filme X"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !s.Has("abc") {
		t.Fatal("Has should be true after Add")
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].InfoHash != "abc" || list[0].Name != "Filme X" {
		t.Fatalf("unexpected list: %+v", list)
	}

	// Re-Add is an idempotent upsert (refreshes magnet/name, no duplicate row).
	if err := s.Add("abc", "magnet:?xt=urn:btih:abc&dn=novo", "Filme X (renomeado)"); err != nil {
		t.Fatalf("re-Add: %v", err)
	}
	list, _ = s.List()
	if len(list) != 1 {
		t.Fatalf("upsert should not duplicate: %+v", list)
	}
	if list[0].Name != "Filme X (renomeado)" {
		t.Fatalf("upsert should refresh name, got %q", list[0].Name)
	}

	if err := s.Remove("abc"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if s.Has("abc") {
		t.Fatal("Has should be false after Remove")
	}
}

func TestSeedsStoreNilSafe(t *testing.T) {
	var s *SeedsStore
	if err := s.Add("x", "m", "n"); err != nil {
		t.Fatalf("nil Add should be no-op, got %v", err)
	}
	if s.Has("x") {
		t.Fatal("nil Has should be false")
	}
	if list, err := s.List(); err != nil || list != nil {
		t.Fatalf("nil List should be (nil,nil), got (%v,%v)", list, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("nil Close should be no-op, got %v", err)
	}
}

func TestSeedsStoreAddEmptyHashNoOp(t *testing.T) {
	s := newSeedsForTest(t)
	if err := s.Add("", "m", "n"); err != nil {
		t.Fatalf("Add empty hash: %v", err)
	}
	list, _ := s.List()
	if len(list) != 0 {
		t.Fatalf("empty hash should not insert, got %+v", list)
	}
}

func TestMatchesSeedTracker(t *testing.T) {
	tests := []struct {
		name      string
		announces []string
		trackers  []string
		want      bool
	}{
		{"empty trackers", []string{"https://jackui.club/announce/pk"}, nil, false},
		{"empty announces", nil, []string{"jackui"}, false},
		{"substring match", []string{"https://AMIGOS-share.club/announce/pk"}, []string{"jackui"}, true},
		{"no match public", []string{"udp://tracker.openbittorrent.com:80/announce"}, []string{"jackui"}, false},
		{"one of many matches", []string{"udp://public/announce", "https://jackui.club/x"}, []string{"jackui"}, true},
		{"empty want never matches", []string{"https://x/announce"}, []string{""}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesSeedTracker(tc.announces, tc.trackers); got != tc.want {
				t.Fatalf("matchesSeedTracker(%v,%v)=%v want %v", tc.announces, tc.trackers, got, tc.want)
			}
		})
	}
}

func TestNormalizeSeedTrackers(t *testing.T) {
	got := normalizeSeedTrackers([]string{" Amigos-Share ", "", "  ", "Other"})
	want := []string{"jackui", "other"}
	if len(got) != len(want) {
		t.Fatalf("len got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if normalizeSeedTrackers(nil) != nil {
		t.Fatal("nil input should give nil")
	}
}

func TestPeersInactiveReturnsError(t *testing.T) {
	s := &Streamer{}
	var h [20]byte
	if _, err := s.Peers(h); err == nil {
		t.Fatal("Peers on an inactive/unknown hash should error")
	}
}

func TestSetSeedTrackersLive(t *testing.T) {
	s := &Streamer{}
	s.SetSeedTrackers([]string{" AMIGOS-share ", ""})
	if len(s.seedTrackers) != 1 || s.seedTrackers[0] != "jackui" {
		t.Fatalf("SetSeedTrackers normalize failed: %v", s.seedTrackers)
	}
	// shouldKeepSeeding with a nil torrent is always false (guard).
	if s.shouldKeepSeeding(nil) {
		t.Fatal("nil torrent should not keep seeding")
	}
}

// DropSeed (ação explícita do usuário) remove o registro PERSISTENTE de
// auto-seed, ao contrário do Drop genérico — senão o torrent voltaria a seedar
// no próximo boot. Regressão do "streamings ativos que reaparecem".
func TestDropSeedRemovesPersisted(t *testing.T) {
	s := NewForTesting()
	seeds := newSeedsForTest(t)
	s.SetSeeds(seeds)

	var h metainfo.Hash
	if err := h.FromHexString("aabbccddeeff00112233445566778899aabbccdd"); err != nil {
		t.Fatal(err)
	}
	if err := seeds.Add(h.HexString(), "magnet:?xt=urn:btih:"+h.HexString(), "X"); err != nil {
		t.Fatal(err)
	}
	if !seeds.Has(h.HexString()) {
		t.Fatal("seed deveria existir antes do DropSeed")
	}
	s.DropSeed(h) // não está ativo no client → Drop é no-op; o que importa é limpar o persistido
	if seeds.Has(h.HexString()) {
		t.Error("DropSeed deveria remover o seed persistido (.seeds.db)")
	}
}
