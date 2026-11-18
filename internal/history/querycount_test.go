package history

import (
	"context"
	"testing"

	"github.com/lgldsilva/jackui/internal/jackett"
)

func TestDistinctQueryCount(t *testing.T) {
	s := newTestStore(t)

	res := func(hash string) []jackett.Result { return []jackett.Result{{Title: "X", InfoHash: hash}} }
	if err := s.Save(context.Background(), "matrix", res("h1"), 1, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), "MATRIX", res("h2"), 1, false); err != nil { // same query, different case
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), "inception", res("h3"), 1, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), "secret", res("h4"), 1, true); err != nil { // incognito → excluded
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), "other user", res("h5"), 2, false); err != nil {
		t.Fatal(err)
	}

	n, err := s.DistinctQueryCount(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("DistinctQueryCount = %d, want 2 (matrix + inception)", n)
	}
}
