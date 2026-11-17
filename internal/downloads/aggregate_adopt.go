package downloads

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/anacrolix/torrent"
)

// adoptTimeout bounds one adoption pass, like the 90s the init paths give the
// same verify. On expiry the files are claimed unverified (verify only saves
// re-fetching bytes already on disk) rather than left waiting forever.
const adoptTimeout = 90 * time.Second

// adoptGroupSiblings claims the group's not-yet-tracked members onto its
// already-live torrent — the O(new files) path that replaces a full per-row
// init for every sibling. It only REGISTERS the work: the piece verify behind
// it queues on the streamer's serialized verify gate and can block for as long
// as another torrent's re-hash takes, and the single tick goroutine must never
// wait on that (it drives every other download's progress, stall detection and
// scheduling, and Stop() waits for it). The members are registered as pending
// like any init, so pause/remove/preempt/Stop cancel the adoption, and the
// next ticks skip re-adopting while it is in flight.
func (w *Worker) adoptGroupSiblings(g Group, state groupState) {
	if g.isWhole() || state.torrent == nil || state.anyPending {
		return
	}
	sub := Group{Key: g.Key, UserID: g.UserID}
	for _, m := range g.Members {
		if _, ok := state.tracked[m.ID]; !ok {
			sub.Members = append(sub.Members, m)
		}
	}
	if len(sub.Members) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = withInitGen(ctx, w.registerGroupPending(sub, cancel))
	// Stop() closes w.stop BEFORE cancelling the pending set, so a registration
	// that raced past its cancel loop is caught here.
	select {
	case <-w.stop:
		cancel()
	default:
	}
	w.doneWG.Add(1)
	go w.runAdoption(ctx, sub, state.torrent)
}

func (w *Worker) runAdoption(ctx context.Context, g Group, t *torrent.Torrent) {
	defer w.doneWG.Done()
	defer w.clearGroupPending(ctx, g)
	ctx, cancel := context.WithTimeout(ctx, adoptTimeout)
	defer cancel()
	w.adoptSiblings(ctx, g, t)
}

// adoptSiblings verifies, claims and tracks each member of g (all untracked)
// on the live torrent t. VerifyFile dedupes per (hash,file) inside the
// streamer, so there is no redundant whole-torrent re-hash. A member whose
// adoption was cancelled (paused/removed/preempted, or the worker stopping) is
// left alone.
func (w *Worker) adoptSiblings(ctx context.Context, g Group, t *torrent.Torrent) {
	files := t.Files()
	hash := t.InfoHash()
	name := t.Name()
	for _, m := range g.Members {
		if !w.adoptionLive(ctx, m.ID) {
			continue
		}
		idx, okIdx := w.resolveFileIndex(&m, files)
		if !okIdx {
			continue
		}
		if err := w.verifyFile(ctx, hash, idx); err != nil {
			log.Printf("downloads: verify adopted file #%d: %v", m.ID, err)
		}
		if !w.adoptionLive(ctx, m.ID) {
			continue
		}
		w.claimAndWant(ctx, m.ID, hash, files[idx])
		w.trackMember(m, t, files[idx], nil, name, hash)
	}
}

// adoptionLive reports whether member id should still be adopted under ctx: not
// cancelled and still the registered init. A DEADLINE is not a cancel — the
// member proceeds unverified (see adoptTimeout).
func (w *Worker) adoptionLive(ctx context.Context, id int) bool {
	if errors.Is(ctx.Err(), context.Canceled) {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, tomb := w.removed[id]
	return !tomb && w.ownsPendingLocked(id, initGen(ctx))
}
