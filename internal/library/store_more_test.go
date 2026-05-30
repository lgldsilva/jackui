package library

import (
	"testing"
)

func TestDeleteIncognito(t *testing.T) {
	s := newTestStore(t)

	s.Upsert(UpsertInput{UserID: 1, InfoHash: "pub1", Magnet: "m:pub1", Name: "Public", Incognito: false})
	s.Upsert(UpsertInput{UserID: 1, InfoHash: "sec1", Magnet: "m:sec1", Name: "Secret", Incognito: true})

	if err := s.DeleteIncognito(1); err != nil {
		t.Fatalf("DeleteIncognito: %v", err)
	}

	// Only the public entry should remain
	list, _ := s.List(1, false, 0)
	if len(list) != 1 || list[0].InfoHash != "pub1" {
		t.Errorf("expected only pub1, got %v", list)
	}
}

func TestDeleteIncognitoOtherUser(t *testing.T) {
	s := newTestStore(t)

	s.Upsert(UpsertInput{UserID: 1, InfoHash: "sec1", Magnet: "m:sec1", Name: "User1 Secret", Incognito: true})
	s.Upsert(UpsertInput{UserID: 2, InfoHash: "sec2", Magnet: "m:sec2", Name: "User2 Secret", Incognito: true})

	// Delete only user 2's incognito
	if err := s.DeleteIncognito(2); err != nil {
		t.Fatalf("DeleteIncognito: %v", err)
	}

	// User 1's incognito should survive
	e, _ := s.GetByHash(1, "sec1")
	if e == nil {
		t.Error("expected user1's incognito to survive")
	}
}

func TestDeleteIncognitoNoIncognito(t *testing.T) {
	s := newTestStore(t)

	s.Upsert(UpsertInput{UserID: 1, InfoHash: "h1", Magnet: "m:h1", Name: "Normal"})
	if err := s.DeleteIncognito(1); err != nil {
		t.Fatalf("DeleteIncognito: %v", err)
	}

	e, _ := s.GetByHash(1, "h1")
	if e == nil {
		t.Error("expected normal entry to survive DeleteIncognito")
	}
}

func TestDeleteIncognitoNilStore(t *testing.T) {
	var s *Store
	if err := s.DeleteIncognito(1); err != nil {
		t.Fatalf("nil DeleteIncognito: %v", err)
	}
}

func TestDeleteAllIncognito(t *testing.T) {
	s := newTestStore(t)

	s.Upsert(UpsertInput{UserID: 1, InfoHash: "pub1", Magnet: "m:pub1", Name: "Public"})
	s.Upsert(UpsertInput{UserID: 1, InfoHash: "sec1", Magnet: "m:sec1", Name: "Secret", Incognito: true})
	s.Upsert(UpsertInput{UserID: 2, InfoHash: "sec2", Magnet: "m:sec2", Name: "Secret2", Incognito: true})

	if err := s.DeleteAllIncognito(); err != nil {
		t.Fatalf("DeleteAllIncognito: %v", err)
	}

	list, _ := s.List(1, true, 0)
	if len(list) != 1 {
		t.Errorf("expected 1 entry (public), got %d", len(list))
	}
}

func TestDeleteAllIncognitoNilStore(t *testing.T) {
	var s *Store
	if err := s.DeleteAllIncognito(); err != nil {
		t.Fatalf("nil DeleteAllIncognito: %v", err)
	}
}

func TestGetByHashPublic(t *testing.T) {
	s := newTestStore(t)

	s.Upsert(UpsertInput{UserID: 1, InfoHash: "pub1", Magnet: "m:pub1", Name: "Public"})
	s.Upsert(UpsertInput{UserID: 1, InfoHash: "sec1", Magnet: "m:sec1", Name: "Secret", Incognito: true})

	e, err := s.GetByHashPublic(1, "pub1")
	if err != nil {
		t.Fatalf("GetByHashPublic: %v", err)
	}
	if e == nil {
		t.Fatal("expected entry for pub1")
	}

	e, err = s.GetByHashPublic(1, "sec1")
	if err != nil {
		t.Fatalf("GetByHashPublic secret: %v", err)
	}
	if e != nil {
		t.Fatal("expected nil for incognito entry via GetByHashPublic")
	}
}

func TestGetByHashPublicNotFound(t *testing.T) {
	s := newTestStore(t)
	e, err := s.GetByHashPublic(1, "nonexistent")
	if err != nil {
		t.Fatalf("GetByHashPublic: %v", err)
	}
	if e != nil {
		t.Fatal("expected nil for nonexistent")
	}
}

func TestRefreshStalePrimary(t *testing.T) {
	s := newTestStore(t)

	// Create entries with primary_file_index=0 (stale)
	s.Upsert(UpsertInput{UserID: 1, InfoHash: "h1", Magnet: "m:h1", Name: "Movie 1"})
	s.Upsert(UpsertInput{UserID: 1, InfoHash: "h2", Magnet: "m:h2", Name: "Movie 2"})
	s.Upsert(UpsertInput{UserID: 1, InfoHash: "h3", Magnet: "m:h3", Name: "Movie 3"})

	lookup := func(infoHash string) (int, bool) {
		switch infoHash {
		case "h1":
			return 0, false // skip — no result
		case "h2":
			return 0, true // skip — 0 is not > 0
		case "h3":
			return 2, true // update to file index 2
		}
		return 0, false
	}

	updated, err := s.RefreshStalePrimary(lookup)
	if err != nil {
		t.Fatalf("RefreshStalePrimary: %v", err)
	}
	if updated != 1 {
		t.Errorf("expected 1 update, got %d", updated)
	}

	e, _ := s.GetByHash(1, "h3")
	if e == nil || e.PrimaryFileIndex != 2 {
		t.Errorf("expected h3 primary_file_index=2, got %d", e.PrimaryFileIndex)
	}
}

func TestDeleteAll(t *testing.T) {
	s := newTestStore(t)

	s.Upsert(UpsertInput{UserID: 1, InfoHash: "h1", Magnet: "m:h1", Name: "U1 Movie"})
	s.Upsert(UpsertInput{UserID: 2, InfoHash: "h2", Magnet: "m:h2", Name: "U2 Movie"})

	n, err := s.DeleteAll(1, false)
	if err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted, got %d", n)
	}

	list1, _ := s.List(1, false, 0)
	if len(list1) != 0 {
		t.Errorf("expected empty for user 1, got %d", len(list1))
	}

	list2, _ := s.List(2, false, 0)
	if len(list2) != 1 {
		t.Errorf("expected 1 for user 2, got %d", len(list2))
	}
}

func TestDeleteAllAdmin(t *testing.T) {
	s := newTestStore(t)

	s.Upsert(UpsertInput{UserID: 1, InfoHash: "h1", Magnet: "m:h1", Name: "U1"})
	s.Upsert(UpsertInput{UserID: 2, InfoHash: "h2", Magnet: "m:h2", Name: "U2"})

	n, err := s.DeleteAll(0, true)
	if err != nil {
		t.Fatalf("DeleteAll admin: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 deleted, got %d", n)
	}
}

func TestListWithLimit(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 10; i++ {
		hash := string(rune('a' + i))
		s.Upsert(UpsertInput{UserID: 1, InfoHash: hash, Magnet: "m:" + hash, Name: "Item"})
	}

	list, _ := s.List(1, false, 3)
	if len(list) != 3 {
		t.Errorf("expected 3, got %d", len(list))
	}
}

func TestListIncludeAll(t *testing.T) {
	s := newTestStore(t)

	s.Upsert(UpsertInput{UserID: 1, InfoHash: "h1", Magnet: "m:h1", Name: "U1"})
	s.Upsert(UpsertInput{UserID: 2, InfoHash: "h2", Magnet: "m:h2", Name: "U2"})

	list, _ := s.List(0, true, 0)
	if len(list) != 2 {
		t.Errorf("expected 2 (includeAll), got %d", len(list))
	}
}

func TestIncognitoExcludedFromList(t *testing.T) {
	s := newTestStore(t)

	s.Upsert(UpsertInput{UserID: 1, InfoHash: "pub", Magnet: "m:pub", Name: "Public"})
	s.Upsert(UpsertInput{UserID: 1, InfoHash: "sec", Magnet: "m:sec", Name: "Secret", Incognito: true})

	list, _ := s.List(1, false, 0)
	if len(list) != 1 || list[0].InfoHash != "pub" {
		t.Errorf("incognito leaked: got %v", list)
	}
}

func TestIncognitoExcludedFromListIncludeAll(t *testing.T) {
	s := newTestStore(t)

	s.Upsert(UpsertInput{UserID: 1, InfoHash: "sec", Magnet: "m:sec", Name: "Secret", Incognito: true})

	list, _ := s.List(0, true, 0)
	if len(list) != 0 {
		t.Errorf("incognito leaked into includeAll: %d items", len(list))
	}
}

func TestGetByIDNotFound(t *testing.T) {
	s := newTestStore(t)
	e, err := s.GetByID(999, 1, false)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if e != nil {
		t.Fatal("expected nil for nonexistent ID")
	}
}
