package transfer

import (
	"context"
	"errors"
	"testing"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

func TestStoreCallsHonourCancelledContext(t *testing.T) {
	s, err := OpenStore(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	live := context.Background()
	id, err := s.Add(live, Pending{Kind: "promote", Src: "/a", Dst: "/b"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Add(ctx, Pending{Kind: "promote", Src: "/c", Dst: "/d"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Add: %v", err)
	}
	if err := s.Remove(ctx, id); !errors.Is(err, context.Canceled) {
		t.Errorf("Remove: %v", err)
	}
	if _, err := s.List(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("List: %v", err)
	}
	if list, _ := s.List(live); len(list) != 1 {
		t.Fatalf("pending after cancelled calls = %d, want 1 untouched", len(list))
	}
}
