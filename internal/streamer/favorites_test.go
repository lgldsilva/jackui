package streamer

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestFavorites(t *testing.T) *FavoritesStore {
	t.Helper()
	s, err := NewFavorites(filepath.Join(t.TempDir(), "fav.db"))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFavoritesAddAndList(t *testing.T) {
	f := newTestFavorites(t)
	if err := f.Add("movie A", "hashA", "magnet:A", "manual", 1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	list, _ := f.List(1, false)
	if len(list) != 1 {
		t.Fatalf("expected 1, got %d", len(list))
	}
	if list[0].Magnet != "magnet:A" {
		t.Errorf("magnet lost: %q", list[0].Magnet)
	}
	if list[0].UserID != 1 {
		t.Errorf("userID lost: %d", list[0].UserID)
	}
}

func TestFavoritesPerUserIsolation(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("A's movie", "hA", "magnet:A", "manual", 1)
	f.Add("B's movie", "hB", "magnet:B", "manual", 2)

	listA, _ := f.List(1, false)
	if len(listA) != 1 || listA[0].UserID != 1 {
		t.Errorf("user 1 leak: %v", listA)
	}
	listB, _ := f.List(2, false)
	if len(listB) != 1 || listB[0].UserID != 2 {
		t.Errorf("user 2 leak: %v", listB)
	}
	listAll, _ := f.List(0, true)
	if len(listAll) != 2 {
		t.Errorf("admin: expected 2, got %d", len(listAll))
	}
}

func TestFavoritesIsFavoriteAnyVsOf(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("movie", "h1", "m:1", "manual", 1)

	if !f.IsFavorite("movie") {
		t.Error("IsFavorite (any) should be true")
	}
	if !f.IsFavoriteOf("movie", 1) {
		t.Error("IsFavoriteOf user 1 should be true")
	}
	if f.IsFavoriteOf("movie", 2) {
		t.Error("IsFavoriteOf user 2 should be false")
	}
}

func TestFavoritesUpsertOnConflict(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("movie", "h1", "magnet:OLD", "manual", 1)
	f.Add("movie", "h1", "magnet:NEW", "auto-5min", 1)

	list, _ := f.List(1, false)
	if len(list) != 1 {
		t.Fatalf("expected 1 row after upsert, got %d", len(list))
	}
	if list[0].Magnet != "magnet:NEW" {
		t.Errorf("magnet not updated: %q", list[0].Magnet)
	}
	if list[0].Reason != "auto-5min" {
		t.Errorf("reason not updated: %q", list[0].Reason)
	}
}

// Regression: legacy favorites DB (no user_id/magnet) must migrate without errors.
// Captures the bug where CREATE INDEX on user_id ran before ALTER added the column.
func TestFavoritesMigrateLegacyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fav_legacy.db")
	{
		legacy, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)")
		if err != nil {
			t.Fatalf("open legacy: %v", err)
		}
		_, err = legacy.Exec(`
			CREATE TABLE favorites (
				name TEXT PRIMARY KEY,
				info_hash TEXT,
				favorited_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				reason TEXT NOT NULL DEFAULT 'manual'
			);
			INSERT INTO favorites(name, info_hash, reason) VALUES('old fav', 'oldhash', 'manual');
		`)
		if err != nil {
			t.Fatalf("seed legacy: %v", err)
		}
		legacy.Close()
	}

	f, err := NewFavorites(path)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	defer f.Close()

	if !f.hasColumn("favorites", "user_id") {
		t.Error("user_id column not added")
	}
	if !f.hasColumn("favorites", "magnet") {
		t.Error("magnet column not added")
	}

	// Legacy row still queryable (with default user_id=0)
	list, _ := f.List(0, false)
	if len(list) != 1 {
		t.Errorf("expected legacy row preserved, got %d rows", len(list))
	}

	// New favorites work after migration
	if err := f.Add("new fav", "newhash", "magnet:new", "manual", 5); err != nil {
		t.Fatalf("Add after migration: %v", err)
	}
	list5, _ := f.List(5, false)
	if len(list5) != 1 || list5[0].Magnet != "magnet:new" {
		t.Errorf("new favorite not isolated to user 5: %v", list5)
	}
}

// Regression: the PlayerModal favoriteAdd args-order bug wrote the literal "manual" into the
// magnet column. NewFavorites must repair those rows on open by reconstructing the magnet from
// info_hash. Idempotent — a subsequent reopen finds zero rows to fix.
func TestFavoritesRecoversManualMagnetCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fav_corrupt.db")

	// First open creates the schema. Seed corrupted rows directly via SQL to simulate prod state.
	{
		f, err := NewFavorites(path)
		if err != nil {
			t.Fatalf("first open: %v", err)
		}
		// Bypass Add() (which sanitises) and inject the corruption shape.
		_, err = f.db.Exec(
			`INSERT INTO favorites(name, info_hash, magnet, reason, user_id) VALUES
				('Corrupt Movie',  'aabbccddeeff00112233445566778899aabbccdd', 'manual', 'manual', 1),
				('No Hash',        '',                                          'manual', 'manual', 1),
				('Healthy',        '1234567890abcdef1234567890abcdef12345678', 'magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678', 'manual', 1)`,
		)
		if err != nil {
			t.Fatalf("seed corrupt rows: %v", err)
		}
		f.Close()
	}

	// Reopen — the recovery UPDATE must rewrite the "manual" magnet into a proper one for
	// rows that have an info_hash. Rows without info_hash stay as "manual" (defensive UI catches them).
	f, err := NewFavorites(path)
	if err != nil {
		t.Fatalf("reopen with recovery: %v", err)
	}
	defer f.Close()

	rows, _ := f.List(1, false)
	got := map[string]string{}
	for _, r := range rows {
		got[r.Name] = r.Magnet
	}
	wantCorruptFixed := "magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd"
	if got["Corrupt Movie"] != wantCorruptFixed {
		t.Errorf("corrupt row not repaired:\n  got  %q\n  want %q", got["Corrupt Movie"], wantCorruptFixed)
	}
	if got["No Hash"] != "manual" {
		// Defensive: can't reconstruct a magnet without an info_hash. Leave it; UI will warn.
		t.Errorf("row without info_hash should be untouched, got %q", got["No Hash"])
	}
	if got["Healthy"] != "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678" {
		t.Errorf("healthy row mutated: %q", got["Healthy"])
	}
}

func TestFavoritesRemoveRespectUser(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("m", "h", "magnet", "manual", 1)

	// User 2 tries to remove user 1's favorite — should not affect it
	f.Remove("m", 2, false)
	if !f.IsFavorite("m") {
		t.Error("favorite should still exist after other user tries to remove")
	}

	// User 1 removes — gone
	f.Remove("m", 1, false)
	if f.IsFavorite("m") {
		t.Error("favorite should be gone after owner removes")
	}
}
