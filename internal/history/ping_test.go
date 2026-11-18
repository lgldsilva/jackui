package history

import (
	"context"
	"errors"
	"testing"
)

func TestPing_ReachesTheDatabase(t *testing.T) {
	if err := newTestStore(t).Ping(context.Background()); err != nil {
		t.Fatalf("Ping on a live pool = %v, want nil", err)
	}
}

func TestPing_HonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newTestStore(t).Ping(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ping with a cancelled context = %v, want context.Canceled", err)
	}
}
