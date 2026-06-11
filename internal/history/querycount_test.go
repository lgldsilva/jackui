package history

import (
	"path/filepath"
	"testing"

	"github.com/lgldsilva/jackui/internal/jackett"
)

func TestDistinctQueryCount(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "hist.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	res := func(hash string) []jackett.Result { return []jackett.Result{{Title: "X", InfoHash: hash}} }
	if err := s.Save("matrix", res("h1"), 1, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("MATRIX", res("h2"), 1, false); err != nil { // same query, different case
		t.Fatal(err)
	}
	if err := s.Save("inception", res("h3"), 1, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("secret", res("h4"), 1, true); err != nil { // incognito → excluded
		t.Fatal(err)
	}
	if err := s.Save("other user", res("h5"), 2, false); err != nil {
		t.Fatal(err)
	}

	n, err := s.DistinctQueryCount(1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("DistinctQueryCount = %d, want 2 (matrix + inception)", n)
	}
}
