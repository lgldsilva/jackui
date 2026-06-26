package transfer

import (
	"path/filepath"
	"testing"
)

func TestStoreAddListRemove(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), ".transfers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id1, err := s.Add(Pending{Kind: "promote", Src: "/a/x.mkv", Dst: "/b/x.mkv", Payload: `{"downloadID":1}`})
	if err != nil || id1 == 0 {
		t.Fatalf("Add 1: id=%d err=%v", id1, err)
	}
	id2, err := s.Add(Pending{Kind: "local-move", Src: "/a/y", Dst: "/b/y"})
	if err != nil || id2 == 0 {
		t.Fatalf("Add 2: id=%d err=%v", id2, err)
	}

	list, err := s.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %d itens, err=%v", len(list), err)
	}
	if list[0].Kind != "promote" || list[0].Src != "/a/x.mkv" || list[0].Payload != `{"downloadID":1}` {
		t.Errorf("item 0 = %+v", list[0])
	}

	if err := s.Remove(id1); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	list, _ = s.List()
	if len(list) != 1 || list[0].ID != id2 {
		t.Fatalf("após remove: %+v", list)
	}
}

func TestStoreNilSafe(t *testing.T) {
	var s *Store
	if id, err := s.Add(Pending{Kind: "promote"}); id != 0 || err != nil {
		t.Errorf("nil Add: id=%d err=%v", id, err)
	}
	if err := s.Remove(1); err != nil {
		t.Errorf("nil Remove: %v", err)
	}
	if l, err := s.List(); l != nil || err != nil {
		t.Errorf("nil List: %v %v", l, err)
	}
	s.Close() // não deve dar panic
}

// Remove(0) é no-op (id 0 = store estava nil quando Add foi chamado).
func TestStoreRemoveZero(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), ".transfers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = s.Add(Pending{Kind: "promote", Src: "a", Dst: "b"})
	if err := s.Remove(0); err != nil {
		t.Fatalf("Remove(0): %v", err)
	}
	if l, _ := s.List(); len(l) != 1 {
		t.Fatalf("Remove(0) não deveria apagar nada, got %d", len(l))
	}
}
