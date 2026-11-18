package streamer

import (
	"context"
	"time"
)

// dbCallTimeout bounds a store call made outside any request (background
// goroutines, eviction, boot). Long enough to ride out a busy pool, short enough
// that a wedged database cannot stall a worker forever.
const dbCallTimeout = 15 * time.Second

// dbCtx returns the ctx for streamer-internal database calls that no request
// owns: background probes/seeding/eviction. It is deliberately NOT derived from a
// request, so a client disconnect can never drop these writes.
func dbCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), dbCallTimeout)
}

// isFavoriteBG is the fail-closed favorite check for eviction/clear paths: a
// timeout or cancelled call returns true (IsFavorite's contract), so protected
// content is never deleted on a fluke.
func (s *Streamer) isFavoriteBG(name string) bool {
	ctx, cancel := dbCtx()
	defer cancel()
	return s.favs.IsFavorite(ctx, name)
}
