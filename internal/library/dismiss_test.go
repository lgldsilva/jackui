package library

import "testing"

func TestDismissKey(t *testing.T) {
	if got := DismissKey("movie", 603); got != "movie:603" {
		t.Errorf("DismissKey = %q, want movie:603", got)
	}
	if DismissKey("movie", 5) == DismissKey("tv", 5) {
		t.Error("same id across kinds must produce distinct keys")
	}
}

func TestDismissRecommendation_PersistAndScope(t *testing.T) {
	s := newTestStore(t)

	if err := s.DismissRecommendation(1, "movie", 603); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	set1, err := s.DismissedRecommendations(1)
	if err != nil {
		t.Fatalf("DismissedRecommendations(1): %v", err)
	}
	if !set1[DismissKey("movie", 603)] {
		t.Errorf("user 1 dismissal missing: %v", set1)
	}
	// Scope: user 2 must not see user 1's dismissal.
	set2, err := s.DismissedRecommendations(2)
	if err != nil {
		t.Fatalf("DismissedRecommendations(2): %v", err)
	}
	if len(set2) != 0 {
		t.Errorf("dismissal leaked across users: %v", set2)
	}
}

func TestDismissRecommendation_Idempotent(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		if err := s.DismissRecommendation(7, "tv", 1399); err != nil {
			t.Fatalf("dismiss #%d: %v", i, err)
		}
	}
	set, err := s.DismissedRecommendations(7)
	if err != nil {
		t.Fatalf("DismissedRecommendations: %v", err)
	}
	if len(set) != 1 || !set[DismissKey("tv", 1399)] {
		t.Errorf("re-dismiss must stay a single row: %v", set)
	}
}

func TestDismissRecommendation_Validation(t *testing.T) {
	s := newTestStore(t)
	if err := s.DismissRecommendation(1, "", 5); err == nil {
		t.Error("empty kind must error")
	}
	if err := s.DismissRecommendation(1, "movie", 0); err == nil {
		t.Error("non-positive tmdbId must error")
	}
}

func TestDismiss_NilStore(t *testing.T) {
	var s *Store
	if err := s.DismissRecommendation(1, "movie", 1); err != nil {
		t.Errorf("nil store dismiss should be a no-op, got %v", err)
	}
	set, err := s.DismissedRecommendations(1)
	if err != nil || len(set) != 0 {
		t.Errorf("nil store → empty set no error; got %v err=%v", set, err)
	}
}
