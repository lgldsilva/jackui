package downloads

import "testing"

func TestHashOwnerIDs(t *testing.T) {
	s := newTestStore(t)
	const h = "0123456789abcdef0123456789abcdef01234567"
	for _, d := range []Download{
		{UserID: 1, InfoHash: h, FileIndex: 0, Name: "A", Magnet: "m"},
		{UserID: 1, InfoHash: h, FileIndex: 1, Name: "A", Magnet: "m"},
		{UserID: 2, InfoHash: h, FileIndex: 0, Name: "A", Magnet: "m"},
		{UserID: 3, InfoHash: "ffffffffffffffffffffffffffffffffffffffff", FileIndex: 0, Name: "B", Magnet: "m"},
	} {
		if _, err := s.Create(d); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	got, err := s.HashOwnerIDs("0123456789ABCDEF0123456789ABCDEF01234567") // case-insensitive input
	if err != nil {
		t.Fatalf("HashOwnerIDs: %v", err)
	}
	if len(got) != 2 || !got[1] || !got[2] {
		t.Fatalf("owners = %v, want {1,2}", got)
	}
	if got, _ := s.HashOwnerIDs("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"); len(got) != 0 {
		t.Fatalf("unknown hash owners = %v, want empty", got)
	}
	var nilStore *Store
	if got, err := nilStore.HashOwnerIDs(h); err != nil || len(got) != 0 {
		t.Fatalf("nil store = %v,%v", got, err)
	}
}
