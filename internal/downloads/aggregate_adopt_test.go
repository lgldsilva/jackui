package downloads

import (
	"context"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// adoptSiblings used to run VerifyFile inline on the tick goroutine with
// context.Background(): a verify queued behind a long re-hash froze every
// other download's progress, stall detection and scheduling, and Stop() hung
// behind it. The tick must hand the adoption off and return.
func TestAdoptGroupSiblings_DoesNotBlockTick(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")
	tor, files, hashHex := prioPack(t)
	d0 := prioRow(t, store, 1, hashHex, 0)
	d1 := prioRow(t, store, 1, hashHex, 1)
	prioTrack(w, d0, tor, files[0])

	entered := make(chan struct{})
	w.verifyFile = func(ctx context.Context, _ metainfo.Hash, _ int) error {
		close(entered)
		<-ctx.Done() // a verify stuck behind the serialized verify queue
		return ctx.Err()
	}
	g := Group{Key: grpKeyOf(d0), UserID: 1, Members: []Download{d0, d1}}
	state := groupState{tracked: map[int]*trackedDL{d0.ID: w.tracked[d0.ID]}, hasTracked: true, torrent: tor}

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		w.adoptGroupSiblings(g, state)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("tick path blocked on the adopted sibling's piece verify")
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("adoption never started verifying the sibling")
	}

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		w.Stop()
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() must cancel an in-flight adoption verify")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (w *Worker) isTrackedForTest(id int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tracked[id] != nil
}

func (w *Worker) isPendingForTest(id int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.pending[id]
	return ok
}

// adoptFixture is a live 4-file torrent with file 0 tracked for user 1 and
// file 1 enqueued but not yet adopted.
func adoptFixture(t *testing.T) (w *Worker, tor *torrent.Torrent, g Group, state groupState, sibling Download) {
	t.Helper()
	store := dlwNewStore(t)
	w = dlwNewWorker(t, store, t.TempDir(), "")
	tor, files, hashHex := prioPack(t)
	d0 := prioRow(t, store, 1, hashHex, 0)
	sibling = prioRow(t, store, 1, hashHex, 1)
	prioTrack(w, d0, tor, files[0])
	g = Group{Key: grpKeyOf(d0), UserID: 1, Members: []Download{d0, sibling}}
	state = groupState{tracked: map[int]*trackedDL{d0.ID: w.tracked[d0.ID]}, hasTracked: true, torrent: tor}
	return w, tor, g, state, sibling
}

// The hand-off still adopts: the sibling ends up tracked and wanted, and its
// pending entry is released.
func TestAdoptGroupSiblings_AdoptsInBackground(t *testing.T) {
	w, tor, g, state, sibling := adoptFixture(t)
	t.Cleanup(w.Stop)
	w.adoptGroupSiblings(g, state)
	waitFor(t, "sibling adoption", func() bool { return w.isTrackedForTest(sibling.ID) && !w.isPendingForTest(sibling.ID) })
	wantPrio(t, tor.Files(), 1, torrent.PiecePriorityNormal, "adopted")
}

// While an adoption is in flight the next tick must not start a second one.
func TestAdoptGroupSiblings_NoOpCases(t *testing.T) {
	w, _, g, state, sibling := adoptFixture(t)
	calls := 0
	w.verifyFile = func(context.Context, metainfo.Hash, int) error { calls++; return nil }

	pending := state
	pending.anyPending = true
	w.adoptGroupSiblings(g, pending)

	noTorrent := state
	noTorrent.torrent = nil
	w.adoptGroupSiblings(g, noTorrent)

	whole := g
	whole.Members = append([]Download{{ID: 555, FileIndex: FileIndexWholeTorrent}}, g.Members...)
	w.adoptGroupSiblings(whole, state)

	allTracked := g
	allTracked.Members = g.Members[:1]
	w.adoptGroupSiblings(allTracked, state)

	w.doneWG.Wait()
	if calls != 0 || w.isPendingForTest(sibling.ID) || w.isTrackedForTest(sibling.ID) {
		t.Fatalf("no adoption expected: verify calls=%d pending=%v tracked=%v",
			calls, w.isPendingForTest(sibling.ID), w.isTrackedForTest(sibling.ID))
	}
}

// A sibling removed while its verify is running must not be claimed or tracked.
func TestAdoptSiblings_RemovedDuringVerifyIsNotAdopted(t *testing.T) {
	w, tor, g, state, sibling := adoptFixture(t)
	t.Cleanup(w.Stop)
	w.verifyFile = func(ctx context.Context, _ metainfo.Hash, _ int) error {
		w.Remove(sibling.ID, sibling.InfoHash)
		return ctx.Err()
	}
	w.adoptGroupSiblings(g, state)
	w.doneWG.Wait()
	if w.isTrackedForTest(sibling.ID) {
		t.Fatal("removed sibling was adopted")
	}
	wantPrio(t, tor.Files(), 1, torrent.PiecePriorityNone, "removed before it was claimed")
}

// A verify that outlives the adoption deadline does not strand the sibling:
// it is claimed unverified. A cancel (worker stopping) does leave it alone.
func TestAdoptSiblings_DeadlineClaimsCancelSkips(t *testing.T) {
	w, tor, g, _, sibling := adoptFixture(t)
	sub := Group{Key: g.Key, UserID: 1, Members: []Download{sibling}}
	w.verifyFile = func(ctx context.Context, _ metainfo.Hash, _ int) error {
		<-ctx.Done()
		return ctx.Err()
	}

	cancelled, cancel := context.WithCancel(withInitGen(context.Background(), w.registerGroupPending(sub, func() {})))
	cancel()
	w.adoptSiblings(cancelled, sub, tor)
	if w.isTrackedForTest(sibling.ID) {
		t.Fatal("a cancelled adoption must not track the sibling")
	}

	expired, cancel2 := context.WithTimeout(withInitGen(context.Background(), w.registerGroupPending(sub, func() {})), time.Millisecond)
	defer cancel2()
	w.adoptSiblings(expired, sub, tor)
	if !w.isTrackedForTest(sibling.ID) {
		t.Fatal("an adoption past its deadline must still claim the sibling")
	}
	wantPrio(t, tor.Files(), 1, torrent.PiecePriorityNormal, "claimed unverified")
}

// An adoption registered after Stop() already swept the pending set is
// cancelled on the spot instead of running for up to adoptTimeout.
func TestAdoptGroupSiblings_AfterStopIsCancelled(t *testing.T) {
	w, _, g, state, sibling := adoptFixture(t)
	w.Stop()
	w.verifyFile = func(ctx context.Context, _ metainfo.Hash, _ int) error {
		<-ctx.Done()
		return ctx.Err()
	}
	w.adoptGroupSiblings(g, state)
	done := make(chan struct{})
	go func() { defer close(done); w.doneWG.Wait() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("adoption started after Stop() was not cancelled")
	}
	if w.isTrackedForTest(sibling.ID) {
		t.Fatal("no adoption after Stop()")
	}
}
