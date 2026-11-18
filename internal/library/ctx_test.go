package library

import (
	"context"
	"errors"
	"testing"
)

func TestStoreCallsHonourCancelledContext(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := map[string]func() error{
		"List":      func() error { _, err := s.List(ctx, 1, false, 10); return err },
		"GetByHash": func() error { _, err := s.GetByHash(ctx, 1, "h"); return err },
		"Upsert": func() error {
			_, err := s.Upsert(ctx, UpsertInput{UserID: 1, InfoHash: "h", Magnet: "magnet:?xt=urn:btih:h", Name: "n"})
			return err
		},
		"DeleteAll":    func() error { _, err := s.DeleteAll(ctx, 1, false); return err },
		"GetByID":      func() error { _, err := s.GetByID(ctx, 1, 1, false); return err },
		"Delete":       func() error { return s.Delete(ctx, 1, 1, false) },
		"UpdateResume": func() error { return s.UpdateResume(ctx, 1, 1, 5, 10, -1, false) },
		"Dismiss":      func() error { return s.DismissRecommendation(ctx, 1, "movie", 603) },
		"Dismissed":    func() error { _, err := s.DismissedRecommendations(ctx, 1); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want context.Canceled", name, err)
		}
	}
}
