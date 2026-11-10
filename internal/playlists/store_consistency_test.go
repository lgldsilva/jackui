package playlists

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

func positions(t *testing.T, s *Store, pid int) []int {
	t.Helper()
	items, err := s.Items(pid, 1, false)
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	out := make([]int, 0, len(items))
	for _, it := range items {
		out = append(out, it.Position)
	}
	sort.Ints(out)
	return out
}

func assertContiguous(t *testing.T, got []int) {
	t.Helper()
	for i, p := range got {
		if p != i {
			t.Fatalf("positions = %v, want contiguous 0..%d", got, len(got)-1)
		}
	}
}

// Regression (#200): Reorder trusted newPos, so moving past the end (or below
// 0) left a gap / negative position the UI could never reorder back.
func TestReorder_ClampsOutOfRangePosition(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.Create(1, "P", "")
	var ids []int
	for i := 0; i < 3; i++ {
		it, err := s.AddItem(p.ID, 1, Item{Title: fmt.Sprint("t", i), Magnet: "m"}, false)
		if err != nil {
			t.Fatalf("AddItem: %v", err)
		}
		ids = append(ids, it.ID)
	}
	if err := s.Reorder(p.ID, ids[0], 1, 99, false); err != nil {
		t.Fatalf("Reorder 99: %v", err)
	}
	assertContiguous(t, positions(t, s, p.ID))
	if it, _ := s.getItem(ids[0]); it.Position != 2 {
		t.Fatalf("moved item position = %d, want 2 (last)", it.Position)
	}
	if err := s.Reorder(p.ID, ids[0], 1, -5, false); err != nil {
		t.Fatalf("Reorder -5: %v", err)
	}
	assertContiguous(t, positions(t, s, p.ID))
	if it, _ := s.getItem(ids[0]); it.Position != 0 {
		t.Fatalf("moved item position = %d, want 0 (first)", it.Position)
	}
}

// Regression (#200): AddItem read MAX(position) outside its transaction, so
// concurrent adds got the same position.
func TestAddItem_ConcurrentAddsGetDistinctPositions(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.Create(1, "P", "")
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.AddItem(p.ID, 1, Item{Title: fmt.Sprint("t", i), Magnet: "m"}, false); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("AddItem: %v", err)
	}
	got := positions(t, s, p.ID)
	if len(got) != n {
		t.Fatalf("items = %d, want %d", len(got), n)
	}
	assertContiguous(t, got)
}
