package audiometa

import (
	"context"
	"errors"
	"testing"
)

func TestStoreCallsHonourCancelledContext(t *testing.T) {
	s := newTestStore(t)
	live := context.Background()
	if err := s.Save(live, "/m/a.mp3", 10, Tags{Title: "t"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := s.Get(ctx, "/m/a.mp3", 10); ok {
		t.Error("Get on a cancelled ctx must miss")
	}
	if err := s.Save(ctx, "/m/b.mp3", 11, Tags{Title: "x"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Save: %v", err)
	}
	if _, ok := s.Get(live, "/m/b.mp3", 11); ok {
		t.Error("cancelled Save must not persist")
	}
	if _, ok := s.Get(live, "/m/a.mp3", 10); !ok {
		t.Error("existing row must be untouched")
	}
}
