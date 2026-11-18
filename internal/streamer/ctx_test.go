package streamer

import (
	"context"
	"errors"
	"testing"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// A done context must stop every favorites call before it reaches Postgres, and
// the fail-closed predicates must stay fail-closed (true) rather than "not a
// favourite", which would let eviction delete protected content.
func TestFavoritesCallsHonourCancelledContext(t *testing.T) {
	f := newTestFavorites(t)
	live := context.Background()
	if err := f.Add(live, "keep", "aa", "magnet:aa", "manual", 1); err != nil {
		t.Fatal(err)
	}
	fl, err := f.CreateFolder(live, 1, "F", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.CreateFolder(live, 1, "O", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := cancelledCtx()
	calls := map[string]func() error{
		"Add":                 func() error { return f.Add(ctx, "x", "bb", "m", "manual", 1) },
		"Remove":              func() error { return f.Remove(ctx, "keep", 1, false) },
		"HashSetForUser":      func() error { _, err := f.HashSetForUser(ctx, 1, false); return err },
		"HiddenFavorites":     func() error { _, err := f.HiddenFavorites(ctx, 1, false); return err },
		"HiddenLocalPaths":    func() error { _, err := f.HiddenLocalPaths(ctx, 1); return err },
		"HiddenLocalPathsAll": func() error { _, err := f.HiddenLocalPathsAll(ctx); return err },
		"SetLocalPathHidden":  func() error { return f.SetLocalPathHidden(ctx, 1, "m", "p", true) },
		"List":                func() error { _, err := f.List(ctx, 1, false, false); return err },
		"ListFolders":         func() error { _, err := f.ListFolders(ctx, 1, true); return err },
		"CreateFolder":        func() error { _, err := f.CreateFolder(ctx, 1, "G", nil, false); return err },
		"SetFolderHidden":     func() error { return f.SetFolderHidden(ctx, 1, fl.ID, true) },
		"GetFolder":           func() error { _, err := f.GetFolder(ctx, 1, fl.ID); return err },
		"RenameFolder":        func() error { return f.RenameFolder(ctx, 1, fl.ID, "R") },
		"MoveFolder":          func() error { return f.MoveFolder(ctx, 1, fl.ID, &other.ID) },
		"DeleteFolder":        func() error { return f.DeleteFolder(ctx, 1, fl.ID) },
		"MoveFavoriteToFolder": func() error {
			return f.MoveFavoriteToFolder(ctx, 1, "keep", &fl.ID)
		},
		"Names":               func() error { _, err := f.Names(ctx); return err },
		"ReconcileMagnets":    func() error { _, err := f.ReconcileMagnets(ctx); return err },
		"magnetlessFavorites": func() error { _, err := f.magnetlessFavorites(ctx); return err },
		"fillMagnet":          func() error { return f.fillMagnet(ctx, "keep", "aa", "m") },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want context.Canceled", name, err)
		}
	}
	// Fail-closed: an unanswerable question is "yes, protected".
	if !f.IsFavorite(ctx, "never-heard-of") || !f.IsFavoriteOf(ctx, "never-heard-of", 1) || !f.IsFavoriteByHash(ctx, "ff") {
		t.Error("IsFavorite* must fail closed (true) on a cancelled ctx")
	}
	got, err := f.List(live, 1, false, true)
	if err != nil || len(got) != 1 || got[0].Name != "keep" {
		t.Fatalf("favorites after cancelled calls = %+v, %v; want untouched", got, err)
	}
	if fl2, err := f.GetFolder(live, 1, fl.ID); err != nil || fl2.Name != "F" || fl2.Hidden {
		t.Fatalf("folder after cancelled calls = %+v, %v; want untouched", fl2, err)
	}
}

func TestSeedsCallsHonourCancelledContext(t *testing.T) {
	s := newSeedsForTest(t)
	live := context.Background()
	if err := s.Add(live, "abc", "m", "n"); err != nil {
		t.Fatal(err)
	}
	ctx := cancelledCtx()
	if err := s.Add(ctx, "def", "m", "n"); !errors.Is(err, context.Canceled) {
		t.Errorf("Add: %v", err)
	}
	if err := s.Remove(ctx, "abc"); !errors.Is(err, context.Canceled) {
		t.Errorf("Remove: %v", err)
	}
	if _, err := s.List(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("List: %v", err)
	}
	if s.Has(ctx, "abc") {
		t.Error("Has on a cancelled ctx must be false")
	}
	if list, err := s.List(live); err != nil || len(list) != 1 {
		t.Fatalf("seeds after cancelled calls = %d, %v; want untouched", len(list), err)
	}
}

func TestMetadataCacheCallsHonourCancelledContext(t *testing.T) {
	c := newTestCache(t)
	live := context.Background()
	info := &TorrentInfo{InfoHash: "aabb", Name: "n", Files: []FileInfo{{Index: 0, Path: "a"}}}
	if err := c.Set(live, info); err != nil {
		t.Fatal(err)
	}
	ctx := cancelledCtx()
	if err := c.Set(ctx, &TorrentInfo{InfoHash: "ccdd", Name: "x"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Set: %v", err)
	}
	if err := c.SetArt(ctx, "aabb", &CachedArt{Source: "tmdb"}); !errors.Is(err, context.Canceled) {
		t.Errorf("SetArt: %v", err)
	}
	if err := c.SetHealth(ctx, "aabb", 5, 5); !errors.Is(err, context.Canceled) {
		t.Errorf("SetHealth: %v", err)
	}
	if c.Get(ctx, "aabb") != nil || c.GetArt(ctx, "aabb") != nil || c.GetHealth(ctx, "aabb") != nil {
		t.Error("reads on a cancelled ctx must miss (nil)")
	}
	if len(c.GetBatch(ctx, []string{"aabb"})) != 0 || len(c.GetSortMeta(ctx, []string{"aabb"})) != 0 {
		t.Error("batch reads on a cancelled ctx must be empty")
	}
	if c.ArtNegativeFresh(ctx, "aabb", 1<<60) {
		t.Error("ArtNegativeFresh on a cancelled ctx must be false")
	}
	if got := c.Get(live, "ccdd"); got != nil {
		t.Fatalf("cancelled Set persisted a row: %+v", got)
	}
	if c.GetArt(live, "aabb") != nil || c.GetHealth(live, "aabb") != nil {
		t.Fatal("cancelled SetArt/SetHealth persisted")
	}
}

// Background writers (probe results, explicit DropSeed) take no request ctx, so a
// caller that has already gone away cannot turn them into silent no-ops.
func TestSaveHealthPersistsWithoutCallerContext(t *testing.T) {
	s := NewForTesting()
	c := newTestCache(t)
	s.SetMetadataCache(c)
	h := scrapeTestHash()
	s.saveHealth(h, 7, 9)
	got := c.GetHealth(context.Background(), h.HexString())
	if got == nil || got.Seeders != 7 || got.Peers != 9 {
		t.Fatalf("saveHealth did not persist: %+v", got)
	}
}

func TestDropSeedRemovesPersistedSeed(t *testing.T) {
	s := NewForTesting()
	seeds, err := NewSeeds(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	s.SetSeeds(seeds)
	h := scrapeTestHash()
	if err := seeds.Add(context.Background(), h.HexString(), "m", "n"); err != nil {
		t.Fatal(err)
	}
	_ = s.DropSeed(h) // not active: ErrTorrentNotActive is fine, the row must still go
	if seeds.Has(context.Background(), h.HexString()) {
		t.Fatal("DropSeed left the persisted seed behind")
	}
}
