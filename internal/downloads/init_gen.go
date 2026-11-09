package downloads

import "context"

// Init generations. The tick/preempt/pause paths cancel an in-flight init and
// clear its pending entry; once the row is rescheduled a NEW init registers
// under the same id. The old goroutine only notices its cancel later — and its
// deferred cleanup used to delete the new init's pending entry (so the new init
// aborted at promotion, could no longer be cancelled by Stop/Remove, and a third
// init started on the next tick). Each init now carries the generation it was
// registered with, and only the current owner may touch the entry.

type initGenKey struct{}

func withInitGen(ctx context.Context, gen uint64) context.Context {
	return context.WithValue(ctx, initGenKey{}, gen)
}

// initGen returns the generation an init goroutine was started with; 0 means
// unknown (legacy callers/tests), which falls back to "any pending entry".
func initGen(ctx context.Context) uint64 {
	if ctx == nil {
		return 0
	}
	g, _ := ctx.Value(initGenKey{}).(uint64)
	return g
}

// ownsPendingLocked reports whether the init with generation gen is still the
// registered in-flight init for id. Caller holds w.mu.
func (w *Worker) ownsPendingLocked(id int, gen uint64) bool {
	if _, ok := w.pending[id]; !ok {
		return false
	}
	return gen == 0 || w.pendingGen[id] == gen
}

// releasePendingLocked is an init goroutine's exit cleanup for id: it clears
// the pending entry and the deletion tombstone only when it still owns them.
// With no pending entry at all (cancelled by Remove/preempt and nothing newer
// registered) the tombstone is cleared too, so it never leaks. A NEWER init's
// entry and tombstone are left alone. Caller holds w.mu.
func (w *Worker) releasePendingLocked(id int, gen uint64) {
	if w.ownsPendingLocked(id, gen) {
		w.clearPendingLocked(id)
		delete(w.removed, id)
		return
	}
	if _, newer := w.pending[id]; !newer {
		delete(w.removed, id)
	}
}

// stillInitOwner reports whether the init goroutine running under ctx is still
// the registered init for id (not cancelled, not superseded).
func (w *Worker) stillInitOwner(ctx context.Context, id int) bool {
	if ctx.Err() != nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ownsPendingLocked(id, initGen(ctx))
}
