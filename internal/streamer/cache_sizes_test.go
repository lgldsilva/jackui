package streamer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sizesFixture is a Streamer over a temp DataDir holding idle multi-file
// entries, with the per-entry walk counted through the measureEntry seam.
type sizesFixture struct {
	s     *Streamer
	dir   string
	walks map[string]int
}

func newSizesFixture(t *testing.T, names ...string) *sizesFixture {
	t.Helper()
	f := &sizesFixture{s: NewForTesting(), dir: t.TempDir(), walks: map[string]int{}}
	f.s.cfg.DataDir = f.dir
	f.s.cfg.MaxCacheSize = 1 << 40 // far above the fixture: nothing is evicted
	f.s.measureEntry = func(path string) (int64, time.Time, error) {
		f.walks[filepath.Base(path)]++
		return dirSizeAndMTime(path)
	}
	for _, n := range names {
		f.addEntry(t, n)
	}
	return f
}

// addEntry creates an entry with a nested file, backdated like an entry that
// has been idle for a while (a just-modified one is deliberately not memoized).
func (f *sizesFixture) addEntry(t *testing.T, name string) {
	t.Helper()
	sub := filepath.Join(f.dir, name, "Season 1")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "e01.mkv"), make([]byte, 64*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	f.backdate(t, name, time.Hour)
}

func (f *sizesFixture) backdate(t *testing.T, name string, age time.Duration) {
	t.Helper()
	at := time.Now().Add(-age)
	if err := os.Chtimes(filepath.Join(f.dir, name), at, at); err != nil {
		t.Fatal(err)
	}
}

func (f *sizesFixture) totalWalks() int {
	n := 0
	for _, c := range f.walks {
		n += c
	}
	return n
}

// The cap check runs every minute; it must not re-walk entries nothing is
// writing to. Before the size cache every pass walked every entry.
func TestEnforceCacheLimit_DoesNotRewalkIdleEntries(t *testing.T) {
	f := newSizesFixture(t, "a", "b", "c")

	for i := 0; i < 5; i++ {
		f.s.enforceCacheLimit()
	}

	if got := f.totalWalks(); got != 3 {
		t.Fatalf("5 cap checks over 3 idle entries walked %d entries, want 3 (one walk each): %v", got, f.walks)
	}
}

func TestEnforceCacheLimit_RewalksWhatMayHaveChanged(t *testing.T) {
	f := newSizesFixture(t, "idle", "touched", "protected", "gone")
	f.s.RegisterDownload("protected")
	f.s.enforceCacheLimit()

	// A change to the entry itself moves its mtime (here: the access stamp the
	// idle reaper writes) and invalidates the record.
	f.backdate(t, "touched", 30*time.Minute)
	if err := os.RemoveAll(filepath.Join(f.dir, "gone")); err != nil {
		t.Fatal(err)
	}
	f.s.enforceCacheLimit()

	want := map[string]int{"idle": 1, "touched": 2, "protected": 2, "gone": 1}
	for name, n := range want {
		if f.walks[name] != n {
			t.Errorf("walks[%q] = %d, want %d", name, f.walks[name], n)
		}
	}
	f.s.sizes.mu.Lock()
	_, keptGone := f.s.sizes.m["gone"]
	_, keptProtected := f.s.sizes.m["protected"]
	f.s.sizes.mu.Unlock()
	if keptGone || keptProtected {
		t.Errorf("size cache kept gone=%v protected=%v, want neither", keptGone, keptProtected)
	}
}

func TestEnforceCacheLimit_SizeCacheExpires(t *testing.T) {
	f := newSizesFixture(t, "a")
	now := time.Now()
	f.s.sizes.now = func() time.Time { return now }

	f.s.enforceCacheLimit()
	now = now.Add(sizeCacheTTL - time.Second)
	f.s.enforceCacheLimit()
	if f.walks["a"] != 1 {
		t.Fatalf("walks before TTL = %d, want 1", f.walks["a"])
	}
	now = now.Add(2 * time.Second)
	f.s.enforceCacheLimit()
	if f.walks["a"] != 2 {
		t.Fatalf("walks after TTL = %d, want 2", f.walks["a"])
	}
}

// An entry modified moments ago is not memoized: a further change within the
// same filesystem timestamp tick would be invisible.
func TestEnforceCacheLimit_RecentlyModifiedEntryNotMemoized(t *testing.T) {
	f := newSizesFixture(t, "fresh")
	f.backdate(t, "fresh", 0)

	f.s.enforceCacheLimit()
	f.s.enforceCacheLimit()

	if f.walks["fresh"] != 2 {
		t.Fatalf("walks = %d, want 2", f.walks["fresh"])
	}
}

// Stats() feeds the cache UI and always measures, whatever the cap check
// memoized; both report the same numbers.
func TestStats_AlwaysMeasures(t *testing.T) {
	f := newSizesFixture(t, "a")
	f.s.enforceCacheLimit()

	exact, err := f.s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if f.walks["a"] != 2 {
		t.Fatalf("Stats() must walk: walks = %d, want 2", f.walks["a"])
	}
	reused, err := f.s.cacheStats(true)
	if err != nil {
		t.Fatal(err)
	}
	if f.walks["a"] != 2 {
		t.Fatalf("cap check after Stats() must reuse: walks = %d, want 2", f.walks["a"])
	}
	if reused.TotalSize != exact.TotalSize || !reused.Entries[0].ModTime.Equal(exact.Entries[0].ModTime) {
		t.Fatalf("memoized snapshot differs: %+v vs %+v", reused.Entries[0], exact.Entries[0])
	}
	if exact.TotalSize == 0 {
		t.Fatal("fixture entry measured as empty")
	}
}

func TestCacheStats_UnmeasurableEntryIsSkipped(t *testing.T) {
	f := newSizesFixture(t, "ok", "broken")
	f.s.measureEntry = func(path string) (int64, time.Time, error) {
		if filepath.Base(path) == "broken" {
			return 0, time.Time{}, errors.New("walk failed")
		}
		return dirSizeAndMTime(path)
	}
	st, err := f.s.cacheStats(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Entries) != 1 || st.Entries[0].Path != "ok" {
		t.Fatalf("entries = %+v, want only ok", st.Entries)
	}
}

// One favorites lookup per snapshot, not one query per entry.
func TestStats_LoadsFavoritesOnce(t *testing.T) {
	f := newSizesFixture(t, "a", "b", "c", "d")
	loads := 0
	f.s.favoriteNames = func() (map[string]bool, error) {
		loads++
		return map[string]bool{"b": true}, nil
	}

	st, err := f.s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("favorites loaded %d times for %d entries, want 1", loads, len(st.Entries))
	}
	for _, e := range st.Entries {
		if e.IsFavorite != (e.Path == "b") {
			t.Errorf("entry %q IsFavorite = %v", e.Path, e.IsFavorite)
		}
	}
}

// When the favorites cannot be loaded every entry counts as one (fail-closed,
// as IsFavorite does), so eviction defers them all to the last tier.
func TestStats_FavoritesFailClosed(t *testing.T) {
	f := newSizesFixture(t, "a", "b")
	f.s.favoriteNames = func() (map[string]bool, error) { return nil, errors.New("db down") }

	st, err := f.s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range st.Entries {
		if !e.IsFavorite {
			t.Errorf("entry %q must count as favorite when the lookup fails", e.Path)
		}
	}
}

func TestFavoritesNames(t *testing.T) {
	var none *FavoritesStore
	if names, err := none.Names(context.Background()); err != nil || len(names) != 0 {
		t.Fatalf("nil store Names = %v, %v", names, err)
	}

	favs := str4NewFavorites(t)
	for _, add := range []struct {
		name string
		user int
	}{{"shared", 1}, {"shared", 2}, {"solo", 3}} {
		if err := favs.Add(context.Background(), add.name, "", "", "test", add.user); err != nil {
			t.Fatal(err)
		}
	}
	names, err := favs.Names(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || !names["shared"] || !names["solo"] {
		t.Fatalf("Names = %v, want {shared, solo}", names)
	}

	// Wired through the Streamer: the store-backed lookup marks the entry.
	f := newSizesFixture(t, "solo", "other")
	f.s.favs = favs
	st, err := f.s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range st.Entries {
		if e.IsFavorite != (e.Path == "solo") {
			t.Errorf("entry %q IsFavorite = %v", e.Path, e.IsFavorite)
		}
	}

	_ = favs.db.Close()
	if _, err := favs.Names(context.Background()); err == nil {
		t.Fatal("Names on a closed pool must fail")
	}
}
