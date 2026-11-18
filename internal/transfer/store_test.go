package transfer

import (
	"context"
	"testing"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

func TestStoreAddListRemove(t *testing.T) {
	s, err := OpenStore(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	id1, err := s.Add(context.Background(), Pending{Kind: "promote", Src: "/a/x.mkv", Dst: "/b/x.mkv", Payload: `{"downloadID":1}`})
	if err != nil || id1 == 0 {
		t.Fatalf("Add 1: id=%d err=%v", id1, err)
	}
	id2, err := s.Add(context.Background(), Pending{Kind: "local-move", Src: "/a/y", Dst: "/b/y"})
	if err != nil || id2 == 0 {
		t.Fatalf("Add 2: id=%d err=%v", id2, err)
	}

	list, err := s.List(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %d items, err=%v", len(list), err)
	}
	if list[0].Kind != "promote" || list[0].Src != "/a/x.mkv" || list[0].Payload != `{"downloadID":1}` {
		t.Errorf("item 0 = %+v", list[0])
	}

	if err := s.Remove(context.Background(), id1); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	list, _ = s.List(context.Background())
	if len(list) != 1 || list[0].ID != id2 {
		t.Fatalf("after remove: %+v", list)
	}
}

func TestStoreNilSafe(t *testing.T) {
	var s *Store
	if id, err := s.Add(context.Background(), Pending{Kind: "promote"}); id != 0 || err != nil {
		t.Errorf("nil Add: id=%d err=%v", id, err)
	}
	if err := s.Remove(context.Background(), 1); err != nil {
		t.Errorf("nil Remove: %v", err)
	}
	if l, err := s.List(context.Background()); l != nil || err != nil {
		t.Errorf("nil List: %v %v", l, err)
	}
}

// Remove(0) is a no-op (id 0 = store was nil when Add was called).
func TestStoreRemoveZero(t *testing.T) {
	s, err := OpenStore(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.Add(context.Background(), Pending{Kind: "promote", Src: "a", Dst: "b"})
	if err := s.Remove(context.Background(), 0); err != nil {
		t.Fatalf("Remove(0): %v", err)
	}
	if l, _ := s.List(context.Background()); len(l) != 1 {
		t.Fatalf("Remove(0) should not delete anything, got %d", len(l))
	}
}
