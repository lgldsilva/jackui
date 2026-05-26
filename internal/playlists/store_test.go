package playlists

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "pl.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndList(t *testing.T) {
	s := newTestStore(t)
	p, err := s.Create(1, "My Movies", "watchlist")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("expected positive ID")
	}
	list, _ := s.List(1, false)
	if len(list) != 1 || list[0].Name != "My Movies" {
		t.Fatalf("List user 1: %v", list)
	}
}

func TestPerUserIsolation(t *testing.T) {
	s := newTestStore(t)
	s.Create(1, "A's list", "")
	s.Create(2, "B's list", "")

	listA, _ := s.List(1, false)
	if len(listA) != 1 || listA[0].Name != "A's list" {
		t.Errorf("user A: got %v", listA)
	}
	listB, _ := s.List(2, false)
	if len(listB) != 1 || listB[0].Name != "B's list" {
		t.Errorf("user B: got %v", listB)
	}
	listAll, _ := s.List(0, true)
	if len(listAll) != 2 {
		t.Errorf("admin: got %d", len(listAll))
	}
}

func TestAddItemsAndPositions(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.Create(1, "Test", "")

	it1, err := s.AddItem(p.ID, 1, Item{Title: "Item 1", Magnet: "magnet:1", InfoHash: "h1"}, false)
	if err != nil {
		t.Fatalf("AddItem 1: %v", err)
	}
	if it1.Position != 0 {
		t.Errorf("first item position: got %d want 0", it1.Position)
	}
	it2, _ := s.AddItem(p.ID, 1, Item{Title: "Item 2", Magnet: "magnet:2", InfoHash: "h2"}, false)
	if it2.Position != 1 {
		t.Errorf("second item position: got %d want 1", it2.Position)
	}
	it3, _ := s.AddItem(p.ID, 1, Item{Title: "Item 3", Magnet: "magnet:3", InfoHash: "h3"}, false)
	if it3.Position != 2 {
		t.Errorf("third item position: got %d want 2", it3.Position)
	}

	items, _ := s.Items(p.ID, 1, false)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].Title != "Item 1" || items[1].Title != "Item 2" || items[2].Title != "Item 3" {
		t.Errorf("order wrong: %v", items)
	}
}

func TestReorderForward(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.Create(1, "T", "")
	a, _ := s.AddItem(p.ID, 1, Item{Title: "A", Magnet: "m:a"}, false)
	b, _ := s.AddItem(p.ID, 1, Item{Title: "B", Magnet: "m:b"}, false)
	c, _ := s.AddItem(p.ID, 1, Item{Title: "C", Magnet: "m:c"}, false)
	_ = b

	// Move A (pos 0) to pos 2 — order should become B, C, A
	if err := s.Reorder(p.ID, a.ID, 1, 2, false); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	items, _ := s.Items(p.ID, 1, false)
	titles := []string{items[0].Title, items[1].Title, items[2].Title}
	want := []string{"B", "C", "A"}
	for i, x := range titles {
		if x != want[i] {
			t.Errorf("pos %d: got %s want %s", i, x, want[i])
		}
	}
	_ = c
}

func TestReorderBackward(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.Create(1, "T", "")
	s.AddItem(p.ID, 1, Item{Title: "A", Magnet: "m:a"}, false)
	s.AddItem(p.ID, 1, Item{Title: "B", Magnet: "m:b"}, false)
	c, _ := s.AddItem(p.ID, 1, Item{Title: "C", Magnet: "m:c"}, false)

	// Move C (pos 2) to pos 0 — order should become C, A, B
	if err := s.Reorder(p.ID, c.ID, 1, 0, false); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	items, _ := s.Items(p.ID, 1, false)
	want := []string{"C", "A", "B"}
	for i, w := range want {
		if items[i].Title != w {
			t.Errorf("pos %d: got %s want %s", i, items[i].Title, w)
		}
	}
}

func TestRemoveItemCompactsPositions(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.Create(1, "T", "")
	s.AddItem(p.ID, 1, Item{Title: "A", Magnet: "m:a"}, false)
	b, _ := s.AddItem(p.ID, 1, Item{Title: "B", Magnet: "m:b"}, false)
	s.AddItem(p.ID, 1, Item{Title: "C", Magnet: "m:c"}, false)

	if err := s.RemoveItem(p.ID, b.ID, 1, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	items, _ := s.Items(p.ID, 1, false)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Position != 0 || items[1].Position != 1 {
		t.Errorf("positions not compacted: %d, %d", items[0].Position, items[1].Position)
	}
	if items[0].Title != "A" || items[1].Title != "C" {
		t.Errorf("wrong items left: %v", items)
	}
}

func TestDeletePlaylistCascadesItems(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.Create(1, "T", "")
	s.AddItem(p.ID, 1, Item{Title: "A", Magnet: "m:a"}, false)
	s.AddItem(p.ID, 1, Item{Title: "B", Magnet: "m:b"}, false)

	if err := s.Delete(p.ID, 1, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Items table should be empty for that playlist (FK cascade)
	rows, _ := s.db.Query(`SELECT COUNT(*) FROM playlist_items WHERE playlist_id = ?`, p.ID)
	defer rows.Close()
	if rows.Next() {
		var n int
		rows.Scan(&n)
		if n != 0 {
			t.Errorf("expected items deleted by cascade, got %d", n)
		}
	}
}

func TestOtherUserCantTouch(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.Create(1, "A's", "")
	s.AddItem(p.ID, 1, Item{Title: "X", Magnet: "m:x"}, false)

	if _, err := s.AddItem(p.ID, 2, Item{Title: "Y", Magnet: "m:y"}, false); err == nil {
		t.Error("user 2 should not be able to add items to user 1's playlist")
	}
	if err := s.Delete(p.ID, 2, false); err == nil {
		t.Error("user 2 should not be able to delete user 1's playlist")
	}
	if err := s.Update(p.ID, 2, "hijacked", "", false); err == nil {
		t.Error("user 2 should not be able to rename user 1's playlist")
	}

	// Admin (includeAll=true) can
	if err := s.Update(p.ID, 0, "admin-set", "", true); err != nil {
		t.Errorf("admin should update: %v", err)
	}
}

func TestItemCount(t *testing.T) {
	s := newTestStore(t)
	p1, _ := s.Create(1, "P1", "")
	p2, _ := s.Create(1, "P2", "")
	s.AddItem(p1.ID, 1, Item{Title: "A", Magnet: "m:a"}, false)
	s.AddItem(p1.ID, 1, Item{Title: "B", Magnet: "m:b"}, false)
	s.AddItem(p2.ID, 1, Item{Title: "C", Magnet: "m:c"}, false)

	list, _ := s.List(1, false)
	counts := map[string]int{}
	for _, p := range list {
		counts[p.Name] = p.ItemCount
	}
	if counts["P1"] != 2 {
		t.Errorf("P1 count: got %d want 2", counts["P1"])
	}
	if counts["P2"] != 1 {
		t.Errorf("P2 count: got %d want 1", counts["P2"])
	}
}
