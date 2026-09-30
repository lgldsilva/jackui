package streamer

import (
	"testing"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

// TestFavoritesSameNameTwoUsers is the regression test for the old schema in
// which favorites had PRIMARY KEY (name) alone: when user B favorited a name
// user A already had, Add's ON CONFLICT(name) DO UPDATE SET user_id =
// excluded.user_id REASSIGNED the single global row to B and A's List silently
// lost the favorite. With PK (user_id, name) each user owns an independent row.
func TestFavoritesSameNameTwoUsers(t *testing.T) {
	f := newTestFavorites(t)
	if err := f.Add("shared movie", "hS", "magnet:S", "manual", 1); err != nil {
		t.Fatalf("user 1 Add: %v", err)
	}
	if err := f.Add("shared movie", "hS", "magnet:S", "manual", 2); err != nil {
		t.Fatalf("user 2 Add: %v", err)
	}

	listA, err := f.List(1, false, false)
	if err != nil {
		t.Fatalf("List user 1: %v", err)
	}
	if len(listA) != 1 || listA[0].UserID != 1 {
		t.Fatalf("user 1 lost the favorite when user 2 added the same name: %+v", listA)
	}
	listB, err := f.List(2, false, false)
	if err != nil {
		t.Fatalf("List user 2: %v", err)
	}
	if len(listB) != 1 || listB[0].UserID != 2 {
		t.Fatalf("user 2 favorite missing: %+v", listB)
	}
	all, err := f.List(0, true, false)
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("admin view: expected one row per user (2), got %d", len(all))
	}
}

// TestFavoritesUnfavoriteByOneKeepsOther: user B unfavoriting a shared name
// must delete only B's row — user A's favorite has to survive.
func TestFavoritesUnfavoriteByOneKeepsOther(t *testing.T) {
	f := newTestFavorites(t)
	f.Add("shared movie", "hS", "magnet:S", "manual", 1)
	f.Add("shared movie", "hS", "magnet:S", "manual", 2)

	if err := f.Remove("shared movie", 2, false); err != nil {
		t.Fatalf("Remove user 2: %v", err)
	}
	if !f.IsFavoriteOf("shared movie", 1) {
		t.Fatal("user 1 favorite must survive user 2's unfavorite")
	}
	if f.IsFavoriteOf("shared movie", 2) {
		t.Fatal("user 2 favorite should be gone after Remove")
	}
	listA, err := f.List(1, false, false)
	if err != nil {
		t.Fatalf("List user 1: %v", err)
	}
	if len(listA) != 1 || listA[0].UserID != 1 {
		t.Fatalf("user 1 should still list exactly their own favorite, got %+v", listA)
	}
}

// TestFavoritesSchemaPKIsUserAndName pins the migration: the favorites PRIMARY
// KEY must span (user_id, name), not name alone. Scoped to current_schema()
// because every dbtest process migrates its own schema in the shared database.
func TestFavoritesSchemaPKIsUserAndName(t *testing.T) {
	pool := dbtest.NewDB(t)
	rows, err := pool.Query(`
		SELECT kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name
		 AND tc.table_schema = kcu.table_schema
		WHERE tc.constraint_type = 'PRIMARY KEY'
		  AND tc.table_name = 'favorites'
		  AND tc.table_schema = current_schema()
		ORDER BY kcu.ordinal_position`)
	if err != nil {
		t.Fatalf("query PK columns: %v", err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan PK column: %v", err)
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate PK columns: %v", err)
	}
	if len(cols) != 2 || cols[0] != "user_id" || cols[1] != "name" {
		t.Fatalf("favorites PK must be (user_id, name), got %v", cols)
	}
}
