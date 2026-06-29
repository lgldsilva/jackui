package streamer

import (
	"database/sql"
	"testing"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

const recHash = "c9a513e47317cd4a8ce2e2f2a2974acbd734ebb3"

// ───── pure-function tests (no DB; run even when Postgres is unconfigured) ─────

func TestNormalizeTitle(t *testing.T) {
	a := normalizeTitle("O.Definitivo.Bau-Parte 01")
	b := normalizeTitle("O Definitivo Bau Parte 01")
	if a != b {
		t.Errorf("normalizeTitle mismatch: %q vs %q", a, b)
	}
	if normalizeTitle("Héllo!! Wörld") == "" {
		t.Error("expected non-empty normalization")
	}
}

func TestBestMagnetMatch_ExactWins(t *testing.T) {
	results := []MagnetMatch{
		{Title: "Some Other Release", Magnet: "magnet:?xt=urn:btih:" + recHash, Seeders: 99},
		{Title: "My.Show.S01", InfoHash: recHash, Seeders: 5},
		{Title: "My Show S01", InfoHash: recHash, Seeders: 20}, // exact (normalized) + more seeders
	}
	m, ok := bestMagnetMatch("My.Show.S01", results)
	if !ok || m.Seeders != 20 {
		t.Fatalf("want exact match with 20 seeders, got ok=%v %+v", ok, m)
	}
}

func TestBestMagnetMatch_SingleUsable(t *testing.T) {
	results := []MagnetMatch{
		{Title: "Totally Different", InfoHash: recHash},
	}
	if _, ok := bestMagnetMatch("Nope Not This", results); !ok {
		t.Error("a single usable result should be accepted")
	}
}

func TestBestMagnetMatch_AmbiguousRejected(t *testing.T) {
	results := []MagnetMatch{
		{Title: "Alpha", InfoHash: recHash},
		{Title: "Beta", InfoHash: recHash},
	}
	if _, ok := bestMagnetMatch("Gamma", results); ok {
		t.Error("ambiguous (multiple non-exact) results must be rejected")
	}
}

func TestBestMagnetMatch_NoUsable(t *testing.T) {
	results := []MagnetMatch{{Title: "X"}, {Title: "Y"}} // no magnet/infoHash
	if _, ok := bestMagnetMatch("X", results); ok {
		t.Error("results without magnet/infoHash must not match")
	}
}

func TestMagnetAndHash(t *testing.T) {
	// magnet present → infoHash extracted from it
	m, h, ok := magnetAndHash(MagnetMatch{Magnet: "magnet:?xt=urn:btih:" + recHash})
	if !ok || h != recHash || m == "" {
		t.Errorf("magnet path: ok=%v h=%q m=%q", ok, h, m)
	}
	// bare infoHash → synthesized tracker-less magnet
	m, h, ok = magnetAndHash(MagnetMatch{InfoHash: recHash})
	if !ok || h != recHash || m != "magnet:?xt=urn:btih:"+recHash {
		t.Errorf("infoHash path: ok=%v h=%q m=%q", ok, h, m)
	}
	// nothing → not ok
	if _, _, ok := magnetAndHash(MagnetMatch{}); ok {
		t.Error("empty match must return ok=false")
	}
}

// ───── DB-backed tests (Postgres; skip when JACKUI_TEST_DATABASE_URL unset) ─────

// recoverEnv opens a favorites + metadata store on ONE shared pool (as in prod),
// so the camada-2 JOIN sees both tables.
func recoverEnv(t *testing.T) (*FavoritesStore, *MetadataCache, *sql.DB) {
	t.Helper()
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1)
	f, err := NewFavorites(pool)
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(f.Close)
	mc, err := NewMetadataCache(pool)
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	t.Cleanup(func() { _ = mc.Close() })
	return f, mc, pool
}

func magnetOf(t *testing.T, f *FavoritesStore, name string) string {
	t.Helper()
	favs, err := f.List(0, true, true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, fav := range favs {
		if fav.Name == name {
			return fav.Magnet
		}
	}
	t.Fatalf("favorite %q not found", name)
	return ""
}

func TestReconcileMagnets_Layer1_FromInfoHash(t *testing.T) {
	f, _, _ := recoverEnv(t)
	if err := f.Add("Layer1", recHash, "", "manual", 1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	n, err := f.ReconcileMagnets()
	if err != nil {
		t.Fatalf("ReconcileMagnets: %v", err)
	}
	if n != 1 {
		t.Errorf("repaired = %d, want 1", n)
	}
	if got := magnetOf(t, f, "Layer1"); got != "magnet:?xt=urn:btih:"+recHash {
		t.Errorf("magnet = %q, want synthesized from info_hash", got)
	}
}

func TestReconcileMagnets_Layer2_FromMetadataByName(t *testing.T) {
	f, mc, _ := recoverEnv(t)
	if err := f.Add("Layer2", "", "", "manual", 1); err != nil { // both empty (inert)
		t.Fatalf("Add: %v", err)
	}
	if err := mc.Set(&TorrentInfo{InfoHash: recHash, Name: "Layer2", TotalSize: 1}); err != nil {
		t.Fatalf("metadata Set: %v", err)
	}
	n, err := f.ReconcileMagnets()
	if err != nil {
		t.Fatalf("ReconcileMagnets: %v", err)
	}
	if n != 1 {
		t.Errorf("repaired = %d, want 1", n)
	}
	if got := magnetOf(t, f, "Layer2"); got != "magnet:?xt=urn:btih:"+recHash {
		t.Errorf("magnet = %q, want adopted from metadata", got)
	}
}

// fakeSearcher implements MagnetSearcher for camada-3 tests.
type fakeSearcher struct{ byName map[string][]MagnetMatch }

func (s fakeSearcher) SearchByName(name string) ([]MagnetMatch, error) {
	return s.byName[name], nil
}

func TestRecoverViaSearch_ConfidentFillsAmbiguousSkips(t *testing.T) {
	f, _, _ := recoverEnv(t)
	if err := f.Add("Good Title", "", "", "manual", 1); err != nil {
		t.Fatalf("Add good: %v", err)
	}
	if err := f.Add("Ambiguous One", "", "", "manual", 1); err != nil {
		t.Fatalf("Add ambiguous: %v", err)
	}
	searcher := fakeSearcher{byName: map[string][]MagnetMatch{
		"Good Title": {{Title: "Good Title", Magnet: "magnet:?xt=urn:btih:" + recHash, Seeders: 10}},
		"Ambiguous One": {
			{Title: "Different A", InfoHash: recHash},
			{Title: "Different B", InfoHash: recHash},
		},
	}}
	n, err := f.RecoverViaSearch(searcher, 25)
	if err != nil {
		t.Fatalf("RecoverViaSearch: %v", err)
	}
	if n != 1 {
		t.Errorf("repaired = %d, want 1 (only the confident match)", n)
	}
	if got := magnetOf(t, f, "Good Title"); got == "" {
		t.Error("confident match should have filled the magnet")
	}
	if got := magnetOf(t, f, "Ambiguous One"); got != "" {
		t.Errorf("ambiguous favorite should stay empty, got %q", got)
	}
}

func TestRecoverViaSearch_NilSearcherNoop(t *testing.T) {
	f, _, _ := recoverEnv(t)
	if n, err := f.RecoverViaSearch(nil, 25); err != nil || n != 0 {
		t.Errorf("nil searcher: n=%d err=%v, want 0/nil", n, err)
	}
}
