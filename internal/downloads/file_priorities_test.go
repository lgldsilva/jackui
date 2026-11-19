package downloads

import (
	"context"
	"testing"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// prioPack is a 4-file torrent shared by two users in the tests below.
func prioPack(t *testing.T) (*torrent.Torrent, []*torrent.File, string) {
	t.Helper()
	tor := wholeSpecTorrent(t, "Pack", [][]string{{"a.bin"}, {"b.bin"}, {"c.bin"}, {"d.bin"}})
	return tor, tor.Files(), tor.InfoHash().HexString()
}

func prioRow(t *testing.T, store *Store, userID int, hashHex string, idx int) Download {
	t.Helper()
	d, err := store.Create(context.Background(), Download{UserID: userID, InfoHash: hashHex, FileIndex: idx, Magnet: "m", Name: "Pack"})
	if err != nil {
		t.Fatalf("Create user %d idx %d: %v", userID, idx, err)
	}
	return *d
}

func prioTrack(w *Worker, d Download, tor *torrent.Torrent, f *torrent.File) {
	f.Download()
	w.mu.Lock()
	w.tracked[d.ID] = &trackedDL{id: d.ID, userID: d.UserID, hash: tor.InfoHash(), torrent: tor, file: f, name: "Pack"}
	w.mu.Unlock()
}

func wantPrio(t *testing.T, files []*torrent.File, idx int, want torrent.PiecePriority, why string) {
	t.Helper()
	if got := files[idx].Priority(); got != want {
		t.Errorf("file %d priority = %v, want %v (%s)", idx, got, want, why)
	}
}

// File priority is one value per file of the shared anacrolix handle, while
// rows are per user. User 1's group init must not cancel a file user 2 is
// already downloading on the same torrent.
func TestApplyFilePriorities_KeepsOtherUsersFileWanted(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")
	tor, files, hashHex := prioPack(t)
	prioTrack(w, prioRow(t, store, 2, hashHex, 0), tor, files[0])

	members := []Download{prioRow(t, store, 1, hashHex, 1), prioRow(t, store, 1, hashHex, 3)}
	g := Group{Key: grpKeyOf(members[0]), UserID: 1, Members: members}
	w.applyFilePriorities(withInitGen(context.Background(), w.registerGroupPending(g, func() {})), g, tor.InfoHash(), tor)

	wantPrio(t, files, 0, torrent.PiecePriorityNormal, "wanted by user 2's tracked row")
	wantPrio(t, files, 1, torrent.PiecePriorityNormal, "selected by user 1")
	wantPrio(t, files, 2, torrent.PiecePriorityNone, "wanted by nobody")
	wantPrio(t, files, 3, torrent.PiecePriorityNormal, "selected by user 1")
}

// Removing one user's row must not stop the same file for another user whose
// row still wants it.
func TestRemove_KeepsFileAnotherUsersRowWants(t *testing.T) {
	w, store, rec := newRemoveWorker(t)
	tor, files, hashHex := prioPack(t)
	mine := prioRow(t, store, 1, hashHex, 0)
	prioTrack(w, mine, tor, files[0])
	prioTrack(w, prioRow(t, store, 2, hashHex, 0), tor, files[0])

	w.Remove(mine.ID, hashHex)

	if calls := rec.calls(); len(calls) != 0 {
		t.Fatalf("torrent must stay while user 2's row is tracked, dropped %v", calls)
	}
	wantPrio(t, files, 0, torrent.PiecePriorityNormal, "still wanted by user 2")
}

// Pausing one file of a torrent whose siblings keep it alive must stop that
// file: the tick untracks the row, and the file's priority has to follow.
func TestTick_PausedMemberStopsItsFileWhileSiblingContinues(t *testing.T) {
	w, store, rec := newRemoveWorker(t)
	t.Cleanup(w.Stop)
	tor, files, hashHex := prioPack(t)
	paused := prioRow(t, store, 1, hashHex, 0)
	kept := prioRow(t, store, 1, hashHex, 1)
	shared := prioRow(t, store, 1, hashHex, 2)
	other := prioRow(t, store, 2, hashHex, 2)
	for _, d := range []Download{paused, kept, shared, other} {
		prioTrack(w, d, tor, files[d.FileIndex])
	}
	// Only `kept` and user 2's row stay active; `paused` and `shared` vanish
	// from the active set the way a pause does.
	for _, d := range []Download{kept, other} {
		if ok, err := store.PromoteToDownloading(context.Background(), d.ID); err != nil || !ok {
			t.Fatalf("promote #%d: ok=%v err=%v", d.ID, ok, err)
		}
	}

	w.tick()

	if calls := rec.calls(); len(calls) != 0 {
		t.Fatalf("torrent must stay while siblings are active, dropped %v", calls)
	}
	wantPrio(t, files, 0, torrent.PiecePriorityNone, "its only row was paused")
	wantPrio(t, files, 1, torrent.PiecePriorityNormal, "sibling still downloading")
	wantPrio(t, files, 2, torrent.PiecePriorityNormal, "user 2 still wants the file user 1 paused")
}

// An init that already marked its file wanted but is not tracked yet must be
// part of the union: another user's group init landing in that window used to
// cancel the file for good (nothing re-raised it once the row was tracked).
func TestApplyFilePriorities_KeepsFileClaimedByInFlightInit(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")
	tor, files, hashHex := prioPack(t)
	other := prioRow(t, store, 2, hashHex, 0)
	w.mu.Lock()
	// Single-row inits register before the hash is known.
	gen := w.setPendingLocked(other.ID, "", func() {})
	w.mu.Unlock()
	otherCtx := withInitGen(context.Background(), gen)
	w.claimAndWant(otherCtx, other.ID, tor.InfoHash(), files[0])

	members := []Download{prioRow(t, store, 1, hashHex, 1)}
	g := Group{Key: grpKeyOf(members[0]), UserID: 1, Members: members}
	w.applyFilePriorities(withInitGen(context.Background(), w.registerGroupPending(g, func() {})), g, tor.InfoHash(), tor)
	wantPrio(t, files, 0, torrent.PiecePriorityNormal, "claimed by user 2's in-flight init")
	wantPrio(t, files, 1, torrent.PiecePriorityNormal, "selected by user 1")

	// The claim dies with the pending entry: once that init is gone untracked,
	// nobody wants file 0.
	w.mu.Lock()
	w.releasePendingLocked(other.ID, gen)
	w.mu.Unlock()
	w.reconcileFilePriorities(tor.InfoHash(), files)
	wantPrio(t, files, 0, torrent.PiecePriorityNone, "claim released, no row left")
	wantPrio(t, files, 1, torrent.PiecePriorityNormal, "still tracked by user 1")
}

// A claim is only recorded for the init that still owns the pending entry.
func TestClaimAndWant_IgnoresSupersededInit(t *testing.T) {
	w, _, _ := newRemoveWorker(t)
	tor, files, _ := prioPack(t)
	w.claimAndWant(context.Background(), 99, tor.InfoHash(), files[0]) // no pending entry at all
	w.mu.Lock()
	gen := w.setPendingLocked(7, "", func() {})
	w.mu.Unlock()
	w.claimAndWant(withInitGen(context.Background(), gen+1), 7, tor.InfoHash(), files[1]) // stale generation
	w.mu.Lock()
	n := len(w.pendingWant)
	w.mu.Unlock()
	if n != 0 {
		t.Fatalf("pendingWant has %d claims, want 0", n)
	}
}

// Removing a row that was still initializing releases the file it had already
// claimed when a sibling keeps the torrent alive.
func TestRemove_PendingRowReleasesItsClaimedFile(t *testing.T) {
	w, store, rec := newRemoveWorker(t)
	tor, files, hashHex := prioPack(t)
	prioTrack(w, prioRow(t, store, 1, hashHex, 1), tor, files[1])
	initing := prioRow(t, store, 1, hashHex, 0)
	cancelled := false
	w.mu.Lock()
	gen := w.setPendingLocked(initing.ID, hashHex, func() { cancelled = true })
	w.mu.Unlock()
	w.claimAndWant(withInitGen(context.Background(), gen), initing.ID, tor.InfoHash(), files[0])

	w.Remove(initing.ID, hashHex)

	if !cancelled {
		t.Error("Remove must cancel the in-flight init")
	}
	if calls := rec.calls(); len(calls) != 0 {
		t.Fatalf("torrent must stay for the tracked sibling, dropped %v", calls)
	}
	wantPrio(t, files, 0, torrent.PiecePriorityNone, "claimed only by the removed row")
	wantPrio(t, files, 1, torrent.PiecePriorityNormal, "sibling untouched")
}

// The tick's untrack pass also releases the claim of an init it cancels.
func TestUntrackVanished_ReleasesClaimsAndPicksDrops(t *testing.T) {
	w, store, _ := newRemoveWorker(t)
	tor, files, hashHex := prioPack(t)
	hash := tor.InfoHash()
	kept := prioRow(t, store, 1, hashHex, 1)
	prioTrack(w, kept, tor, files[1])
	initing := prioRow(t, store, 1, hashHex, 0)
	w.mu.Lock()
	gen := w.setPendingLocked(initing.ID, hashHex, func() {})
	// A tracked row of ANOTHER torrent nobody wants any more, and one whose
	// hash never resolved.
	gone := hashFromHex(t, testHashHex)
	w.tracked[9001] = &trackedDL{id: 9001, hash: gone, name: "Gone"}
	w.tracked[9002] = &trackedDL{id: 9002, name: "Hashless"}
	w.mu.Unlock()
	w.claimAndWant(withInitGen(context.Background(), gen), initing.ID, hash, files[0])

	toDrop, released := w.untrackVanished(map[int]bool{kept.ID: true}, map[metainfo.Hash]bool{hash: true})
	if len(toDrop) != 1 || toDrop[0] != gone {
		t.Fatalf("toDrop = %v, want only %v", toDrop, gone)
	}
	if got := released[hash]; len(got) != 1 || got[0] != files[0] {
		t.Fatalf("released = %v, want file 0 of the surviving torrent", released)
	}
	w.reconcileReleased(released)
	wantPrio(t, files, 0, torrent.PiecePriorityNone, "its init was cancelled by the tick")
	wantPrio(t, files, 1, torrent.PiecePriorityNormal, "still active")
}

// Reconciling leaves deliberate priority labels and unrelated input alone.
func TestReconcileFilePriorities_EdgeCases(t *testing.T) {
	w, store, _ := newRemoveWorker(t)
	tor, files, hashHex := prioPack(t)
	prioTrack(w, prioRow(t, store, 1, hashHex, 0), tor, files[0])
	files[0].SetPriority(torrent.PiecePriorityHigh)
	files[2].Download()

	w.reconcileFilePriorities(tor.InfoHash(), nil)
	wantPrio(t, files, 2, torrent.PiecePriorityNormal, "empty candidate list is a no-op")

	w.reconcileFilePriorities(tor.InfoHash(), []*torrent.File{nil, files[0], files[2]})
	wantPrio(t, files, 0, torrent.PiecePriorityHigh, "a wanted file keeps its label")
	wantPrio(t, files, 2, torrent.PiecePriorityNone, "wanted by no row")

	// A wanted file found cancelled (lowered behind the row's back) is raised again.
	files[0].SetPriority(torrent.PiecePriorityNone)
	w.reconcileFilePriorities(tor.InfoHash(), files[:1])
	wantPrio(t, files, 0, torrent.PiecePriorityNormal, "wanted by a tracked row")

	r := releasedFiles{}
	r.add(metainfo.Hash{}, files[0])
	r.add(tor.InfoHash(), nil)
	if len(r) != 0 {
		t.Fatalf("zero hash / nil file must not be recorded, got %v", r)
	}
}
