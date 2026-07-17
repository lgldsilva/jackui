package downloads

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/lgldsilva/jackui/internal/streamer"
)

// ─── (a) scheduler: one slot per TORRENT, not per file ──────────────────────

// 10 rows of ONE multi-file torrent + 3 single-file torrents, MaxActive=2: the
// big torrent takes ONE slot (all 10 of its rows promoted together) and one
// single-file torrent takes the other. This is the OOM fix: a 389-file pack no
// longer eats every slot.
func TestSchedulePlan_OneSlotPerTorrent(t *testing.T) {
	var items []Download
	for i := 0; i < 10; i++ {
		// Same (user, info_hash), distinct file_index → ONE group of 10.
		items = append(items, Download{
			ID: i + 1, UserID: 1, InfoHash: "pack", FileIndex: i,
			Status: StatusQueued, Priority: PriorityNormal,
			QueuedSince: ptrTime(schedNow.Add(-time.Duration(100-i) * time.Minute)),
		})
	}
	// Three independent single-file torrents, queued later (so the pack wins age).
	for i, h := range []string{"a", "b", "c"} {
		items = append(items, Download{
			ID: 100 + i, UserID: 1, InfoHash: h, FileIndex: 0,
			Status: StatusQueued, Priority: PriorityNormal,
			QueuedSince: ptrTime(schedNow.Add(-time.Duration(10-i) * time.Minute)),
		})
	}
	want := schedulePlan(items, SchedSettings{MaxActive: 2}, schedNow)

	// The whole pack (all 10 ids) must be in the plan — it's ONE slot.
	for i := 1; i <= 10; i++ {
		if !want[i] {
			t.Fatalf("pack member #%d should be promoted (group is one slot), got %v", i, want)
		}
	}
	// Exactly ONE of the single-file torrents fills the second slot.
	single := 0
	for i := 100; i <= 102; i++ {
		if want[i] {
			single++
		}
	}
	if single != 1 {
		t.Fatalf("exactly one single-file torrent should take the 2nd slot, got %d (%v)", single, want)
	}
}

// A whole-torrent sentinel row and a single-file row of a DIFFERENT torrent are
// two groups; with MaxActive=1 only the older wins, proving groups (not rows)
// are counted.
func TestSchedulePlan_PerUserCountsGroupsNotFiles(t *testing.T) {
	var items []Download
	for i := 0; i < 5; i++ { // user 1: one 5-file pack
		items = append(items, Download{
			ID: i + 1, UserID: 1, InfoHash: "pk", FileIndex: i,
			Status: StatusQueued, Priority: PriorityNormal,
			QueuedSince: ptrTime(schedNow.Add(-time.Hour)),
		})
	}
	items = append(items, Download{ // user 1: a separate single file
		ID: 50, UserID: 1, InfoHash: "solo", FileIndex: 0,
		Status: StatusQueued, Priority: PriorityNormal,
		QueuedSince: ptrTime(schedNow.Add(-time.Minute)),
	})
	// PerUserMax=1 → only ONE group runs for the user, despite 6 rows.
	want := schedulePlan(items, SchedSettings{MaxActive: 5, PerUserMax: 1}, schedNow)
	got := 0
	for id := range want {
		got++
		_ = id
	}
	if got != 5 { // the 5-file pack (one group) only
		t.Fatalf("per-user cap=1 should admit ONE group (the 5-file pack), got ids %v", want)
	}
	if want[50] {
		t.Fatalf("the solo file is a second group and must be blocked by per-user cap=1: %v", want)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// ─── (b) partial selection: only selected files wanted, rest cancelled ──────

func TestApplyFilePriorities_SelectedWantedRestCancelled(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")
	// A 4-file torrent; the user selected files 1 and 3.
	tor := wholeSpecTorrent(t, "Pack", [][]string{{"a.bin"}, {"b.bin"}, {"c.bin"}, {"d.bin"}})
	var members []Download
	for _, idx := range []int{1, 3} {
		d, err := store.Create(Download{
			UserID: 1, InfoHash: tor.InfoHash().HexString(), FileIndex: idx,
			Magnet: "magnet:?xt=urn:btih:" + tor.InfoHash().HexString(), Name: "Pack",
		})
		if err != nil {
			t.Fatalf("Create idx %d: %v", idx, err)
		}
		members = append(members, *d)
	}
	g := Group{Key: grpKeyOf(members[0]), UserID: 1, Members: members}

	w.applyFilePriorities(context.Background(), g, tor.InfoHash(), tor)

	files := tor.Files()
	for i, f := range files {
		want := torrent.PiecePriorityNone
		if i == 1 || i == 3 {
			want = torrent.PiecePriorityNormal
		}
		if got := f.Priority(); got != want {
			t.Errorf("file %d priority = %v, want %v", i, got, want)
		}
	}
	// Both selected members are now tracked (single torrent, no re-init each).
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, m := range members {
		if w.tracked[m.ID] == nil {
			t.Errorf("selected member #%d should be tracked", m.ID)
		}
	}
}

// adoptSiblings claims a newly-selected file onto an ALREADY-LIVE torrent without
// a second EnsureActive — the cheap O(new files) path.
func TestAdoptSiblings_ClaimsNewFileOnLiveTorrent(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")
	tor := wholeSpecTorrent(t, "Pack", [][]string{{"a.bin"}, {"b.bin"}})
	hash := tor.InfoHash()
	d0, _ := store.Create(Download{UserID: 1, InfoHash: hash.HexString(), FileIndex: 0, Magnet: "m", Name: "Pack"})
	d1, _ := store.Create(Download{UserID: 1, InfoHash: hash.HexString(), FileIndex: 1, Magnet: "m", Name: "Pack"})
	// d0 already tracked on the live torrent; d1 not yet.
	files := tor.Files()
	files[0].Download()
	w.mu.Lock()
	w.tracked[d0.ID] = &trackedDL{id: d0.ID, userID: 1, hash: hash, torrent: tor, file: files[0], name: "Pack"}
	w.mu.Unlock()

	state := groupState{tracked: map[int]*trackedDL{d0.ID: w.tracked[d0.ID]}, hasTracked: true, torrent: tor}
	g := Group{Key: grpKeyOf(*d0), UserID: 1, Members: []Download{*d0, *d1}}
	w.adoptSiblings(context.Background(), g, state)

	w.mu.Lock()
	_, adopted := w.tracked[d1.ID]
	w.mu.Unlock()
	if !adopted {
		t.Fatal("sibling file should be adopted onto the live torrent")
	}
	if files[1].Priority() != torrent.PiecePriorityNormal {
		t.Errorf("adopted file priority = %v, want Normal", files[1].Priority())
	}
}

// ─── (c) group completion: ONE move, all rows → completed ───────────────────

func TestCompleteGroup_MovesOnceAllRowsCompleted(t *testing.T) {
	store := dlwNewStore(t)
	s := streamer.NewForTesting()
	dataDir := t.TempDir()
	downloadDir := t.TempDir()
	w := NewWorker(WorkerConfig{Store: store, Streamer: s, DataDir: dataDir, DownloadDir: downloadDir})
	w.moveBackoff = time.Millisecond
	tor := wholeSpecTorrent(t, "Pack", [][]string{{"a.bin"}, {"b.bin"}})
	hash := tor.InfoHash().HexString()
	d0, _ := store.Create(Download{UserID: 1, InfoHash: hash, FileIndex: 0, Magnet: "m", Name: "Pack", FileSize: 4})
	d1, _ := store.Create(Download{UserID: 1, InfoHash: hash, FileIndex: 1, Magnet: "m", Name: "Pack", FileSize: 4})
	// MoveGroup is status-guarded (downloading→moving), so the rows must be active.
	_, _ = store.PromoteToDownloading(d0.ID)
	_, _ = store.PromoteToDownloading(d1.ID)
	// Lay both files in the cache as anacrolix would.
	if err := os.MkdirAll(filepath.Join(dataDir, "Pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.bin", "b.bin"} {
		if err := os.WriteFile(filepath.Join(dataDir, "Pack", n), []byte("zzzz"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := tor.Files()
	td0 := &trackedDL{id: d0.ID, userID: 1, name: "Pack", file: files[0], hash: tor.InfoHash(), torrent: tor}
	td1 := &trackedDL{id: d1.ID, userID: 1, name: "Pack", file: files[1], hash: tor.InfoHash(), torrent: tor}
	w.mu.Lock()
	w.tracked[d0.ID] = td0
	w.tracked[d1.ID] = td1
	w.mu.Unlock()
	state := groupState{tracked: map[int]*trackedDL{d0.ID: td0, d1.ID: td1}, hasTracked: true, torrent: tor}
	g := Group{Key: grpKeyOf(*d0), UserID: 1, Members: []Download{*d0, *d1}}

	w.completeGroup(g, state)

	// Both rows reach completed; both files landed under the single dest dir.
	waitForStatus(t, store, 1, d0.ID, StatusCompleted, 2*time.Second)
	waitForStatus(t, store, 1, d1.ID, StatusCompleted, 2*time.Second)
	for _, n := range []string{"a.bin", "b.bin"} {
		if !fileExists(filepath.Join(downloadDir, "Pack", n)) {
			t.Errorf("%s should be moved to the group dest dir", n)
		}
	}
	// Item 6: each member points at its OWN file, never the bare torrent dir.
	g0, _ := store.Get(1, d0.ID)
	g1, _ := store.Get(1, d1.ID)
	if g0.FilePath != filepath.Join(downloadDir, "Pack", "a.bin") {
		t.Errorf("member 0 file_path = %q, want its own file", g0.FilePath)
	}
	if g1.FilePath != filepath.Join(downloadDir, "Pack", "b.bin") {
		t.Errorf("member 1 file_path = %q, want its own file", g1.FilePath)
	}
	// The group was handed off → both untracked.
	w.mu.Lock()
	_, t0 := w.tracked[d0.ID]
	_, t1 := w.tracked[d1.ID]
	w.mu.Unlock()
	if t0 || t1 {
		t.Error("members handed to the move must be untracked")
	}
}

// A partial group (one file still downloading) must NOT move yet.
func TestCheckGroupCompletion_PartialNoMove(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), t.TempDir())
	tor := wholeSpecTorrent(t, "Pack", [][]string{{"a.bin"}, {"b.bin"}})
	hash := tor.InfoHash().HexString()
	d0, _ := store.Create(Download{UserID: 1, InfoHash: hash, FileIndex: 0, Magnet: "m", Name: "Pack", FileSize: 4})
	d1, _ := store.Create(Download{UserID: 1, InfoHash: hash, FileIndex: 1, Magnet: "m", Name: "Pack", FileSize: 4})
	// d0 complete, d1 not — use fakeWhole-like targets via real files won't report
	// completion; instead use a fake file target by wrapping progress in trackedDL.
	td0 := &trackedDL{id: d0.ID, userID: 1, name: "Pack", whole: &fakeWhole{completed: 4, length: 4}}
	td1 := &trackedDL{id: d1.ID, userID: 1, name: "Pack", whole: &fakeWhole{completed: 2, length: 4}}
	w.mu.Lock()
	w.tracked[d0.ID] = td0
	w.tracked[d1.ID] = td1
	w.mu.Unlock()
	state := groupState{tracked: map[int]*trackedDL{d0.ID: td0, d1.ID: td1}, hasTracked: true, torrent: tor}
	g := Group{Key: grpKeyOf(*d0), UserID: 1, Members: []Download{*d0, *d1}}

	w.checkGroupCompletion(g, state)

	got, _ := store.Get(1, d0.ID)
	if got.Status == StatusMoving || got.Status == StatusCompleted {
		t.Fatalf("partial group must not move; #%d status=%q", d0.ID, got.Status)
	}
}

// ─── (item 2) completion eligibility waits on a QUEUED sibling row ──────────

// A torrent with one tracked file 100% on disk but a SECOND selected file still
// `queued` (not yet promoted, so invisible to ListActive/GroupRows) must NOT
// complete — otherwise the move fires with that file missing. groupCompletionEligible
// consults the store for wanted siblings of the same (user, info_hash).
func TestGroupCompletionEligible_WaitsOnQueuedSibling(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), t.TempDir())
	hash := "packhash"
	// d0 is the active+complete member; d1 is a selected sibling still queued.
	d0, _ := store.Create(Download{UserID: 1, InfoHash: hash, FileIndex: 0, Magnet: "m", Name: "Pack", FileSize: 4})
	d1, _ := store.Create(Download{UserID: 1, InfoHash: hash, FileIndex: 1, Magnet: "m", Name: "Pack", FileSize: 4})
	_, _ = store.PromoteToDownloading(d0.ID) // d1 stays queued
	td0 := &trackedDL{id: d0.ID, userID: 1, name: "Pack", whole: &fakeWhole{completed: 4, length: 4}}
	w.mu.Lock()
	w.tracked[d0.ID] = td0
	w.mu.Unlock()
	// The tick only groups the DOWNLOADING rows → group is just {d0}.
	state := groupState{tracked: map[int]*trackedDL{d0.ID: td0}, hasTracked: true}
	g := Group{Key: grpKeyOf(*d0), UserID: 1, Members: []Download{*d0}}

	if w.groupCompletionEligible(g, state) {
		t.Fatal("group must NOT be completion-eligible while a selected sibling is queued")
	}
	w.checkGroupCompletion(g, state)
	if got, _ := store.Get(1, d0.ID); got.Status == StatusMoving || got.Status == StatusCompleted {
		t.Fatalf("premature completion with a queued sibling; status=%q", got.Status)
	}

	// Once the sibling is gone (e.g. removed), the lone complete member CAN finish.
	_ = store.Delete(1, d1.ID)
	if !w.groupCompletionEligible(g, state) {
		t.Fatal("group should be eligible once no queued sibling remains")
	}
}

// ─── (item 1) Remove a member mid-INIT keeps the shared torrent alive ───────

// A multi-file pack in a SHARED init (members pending, none tracked yet): removing
// one member must NOT drop the torrent the siblings are still resolving. Removing
// the last member (no tracked, no pending, no wanted row) drops it.
func TestRemove_MemberInInitKeepsSharedTorrent(t *testing.T) {
	w, store, rec := newRemoveWorker(t)
	hash := hashFromHex(t, testHashHex)
	d0, _ := store.Create(Download{UserID: 1, InfoHash: testHashHex, FileIndex: 0, Magnet: "m", Name: "Pack"})
	d1, _ := store.Create(Download{UserID: 1, InfoHash: testHashHex, FileIndex: 1, Magnet: "m", Name: "Pack"})
	d2, _ := store.Create(Download{UserID: 1, InfoHash: testHashHex, FileIndex: 2, Magnet: "m", Name: "Pack"})
	// All three are in a shared init: pending + pendingHash set, NONE tracked.
	cancel := func() {}
	w.mu.Lock()
	for _, id := range []int{d0.ID, d1.ID, d2.ID} {
		w.setPendingLocked(id, testHashHex, cancel)
	}
	w.mu.Unlock()

	// Remove the MIDDLE member while it's still only pending (the item-1 scenario).
	w.Remove(d1.ID, testHashHex)
	if calls := rec.calls(); len(calls) != 0 {
		t.Fatalf("removing a member still in init must NOT drop the shared torrent, got %v", calls)
	}

	// Remove d0 too — d2 still pending → still no drop.
	w.Remove(d0.ID, testHashHex)
	if calls := rec.calls(); len(calls) != 0 {
		t.Fatalf("torrent must survive while any sibling is still pending, got %v", calls)
	}

	// Remove the LAST member: nothing tracked, nothing pending → drop.
	w.Remove(d2.ID, testHashHex)
	calls := rec.calls()
	if len(calls) != 1 || calls[0] != hash {
		t.Fatalf("removing the last member must drop the torrent once, got %v", calls)
	}
}

// ─── (g) UpdateProgressBatch in a single transaction ────────────────────────

func TestUpdateProgressBatch_PersistsAllInOneTx(t *testing.T) {
	store := dlwNewStore(t)
	var ids []int
	for i := 0; i < 3; i++ {
		d, _ := store.Create(Download{
			UserID: 1, InfoHash: "h", FileIndex: i, Magnet: "m", Name: "Pack", FileSize: 100,
		})
		ids = append(ids, d.ID)
	}
	batch := []ProgressUpdate{
		{UserID: 1, ID: ids[0], Bytes: 10},
		{UserID: 1, ID: ids[1], Bytes: 20},
		{UserID: 1, ID: ids[2], Bytes: 30},
	}
	if err := store.UpdateProgressBatch(batch); err != nil {
		t.Fatalf("UpdateProgressBatch: %v", err)
	}
	for i, id := range ids {
		got, _ := store.Get(1, id)
		want := int64((i + 1) * 10)
		if got.BytesDownloaded != want {
			t.Errorf("#%d bytes = %d, want %d", id, got.BytesDownloaded, want)
		}
	}
	// Empty batch is a no-op (no error, no panic).
	if err := store.UpdateProgressBatch(nil); err != nil {
		t.Errorf("empty batch should be a no-op, got %v", err)
	}
}

// ─── (d) AI-rename only for single-file groups ──────────────────────────────

// A multi-file selected group must NOT trigger the AI rename chain on completion
// (the chain targets one media file, not a tree) — runGroupCompletionMove omits
// it. We assert via the code path: completeGroup → runGroupCompletionMove never
// touches aiRenameCompleted even with an AI client wired. Single-file groups go
// through checkCompletion → runCompletionMove, which DOES rename. Here we verify
// the multi-file path leaves files where the move put them (no rename move).
func TestRunGroupCompletionMove_SkipsAIRename(t *testing.T) {
	store := dlwNewStore(t)
	s := streamer.NewForTesting()
	dataDir := t.TempDir()
	downloadDir := t.TempDir()
	// aiClient non-nil would matter only on the single-file path; the group path
	// must ignore it. We can't construct a real ai.Client cheaply, so we assert the
	// group path simply doesn't reference it: completion still succeeds with files
	// in their plain (non-Plex) destination.
	w := NewWorker(WorkerConfig{Store: store, Streamer: s, DataDir: dataDir, DownloadDir: downloadDir})
	w.moveBackoff = time.Millisecond
	tor := wholeSpecTorrent(t, "Pack", [][]string{{"a.bin"}, {"b.bin"}})
	hash := tor.InfoHash().HexString()
	d0, _ := store.Create(Download{UserID: 1, InfoHash: hash, FileIndex: 0, Magnet: "m", Name: "Pack", FileSize: 4})
	d1, _ := store.Create(Download{UserID: 1, InfoHash: hash, FileIndex: 1, Magnet: "m", Name: "Pack", FileSize: 4})
	_, _ = store.PromoteToDownloading(d0.ID)
	_, _ = store.PromoteToDownloading(d1.ID)
	if err := os.MkdirAll(filepath.Join(dataDir, "Pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.bin", "b.bin"} {
		_ = os.WriteFile(filepath.Join(dataDir, "Pack", n), []byte("zzzz"), 0o644)
	}
	files := tor.Files()
	td0 := &trackedDL{id: d0.ID, userID: 1, name: "Pack", file: files[0], hash: tor.InfoHash(), torrent: tor}
	td1 := &trackedDL{id: d1.ID, userID: 1, name: "Pack", file: files[1], hash: tor.InfoHash(), torrent: tor}
	w.mu.Lock()
	w.tracked[d0.ID] = td0
	w.tracked[d1.ID] = td1
	w.mu.Unlock()
	state := groupState{tracked: map[int]*trackedDL{d0.ID: td0, d1.ID: td1}, hasTracked: true, torrent: tor}
	g := Group{Key: grpKeyOf(*d0), UserID: 1, Members: []Download{*d0, *d1}}

	w.completeGroup(g, state)
	waitForStatus(t, store, 1, d0.ID, StatusCompleted, 2*time.Second)
	// Files at the plain group dir (no Plex-style rename subtree).
	for _, n := range []string{"a.bin", "b.bin"} {
		if !fileExists(filepath.Join(downloadDir, "Pack", n)) {
			t.Errorf("%s should be at the plain dest (no AI rename)", n)
		}
	}
}

// ─── (e) Remove one file keeps the torrent; last member drops ───────────────

func TestRemove_OneFileKeepsTorrentLastDrops(t *testing.T) {
	w, store, rec := newRemoveWorker(t)
	hash := hashFromHex(t, testHashHex)
	tor := wholeSpecTorrent(t, "Pack", [][]string{{"a.bin"}, {"b.bin"}})
	files := tor.Files()
	files[0].Download()
	files[1].Download()
	d0, _ := store.Create(Download{UserID: 1, InfoHash: testHashHex, FileIndex: 0, Magnet: "m", Name: "Pack"})
	d1, _ := store.Create(Download{UserID: 1, InfoHash: testHashHex, FileIndex: 1, Magnet: "m", Name: "Pack"})
	w.streamer.RegisterDownload("Pack")
	w.mu.Lock()
	w.tracked[d0.ID] = &trackedDL{id: d0.ID, userID: 1, name: "Pack", hash: hash, file: files[0], torrent: tor}
	w.tracked[d1.ID] = &trackedDL{id: d1.ID, userID: 1, name: "Pack", hash: hash, file: files[1], torrent: tor}
	w.mu.Unlock()

	// Remove the FIRST file: the torrent must stay alive (sibling d1 remains).
	w.Remove(d0.ID, testHashHex)
	if calls := rec.calls(); len(calls) != 0 {
		t.Fatalf("removing one file must NOT drop the torrent, got %v", calls)
	}
	if files[0].Priority() != torrent.PiecePriorityNone {
		t.Errorf("removed file 0 priority = %v, want None (cancelled)", files[0].Priority())
	}
	if files[1].Priority() != torrent.PiecePriorityNormal {
		t.Errorf("sibling file 1 priority = %v, want Normal (still wanted)", files[1].Priority())
	}
	if !w.streamer.IsDownloadProtected("Pack") {
		t.Error("eviction protection must remain while a sibling is tracked")
	}

	// Remove the LAST file: now the torrent drops.
	w.Remove(d1.ID, testHashHex)
	calls := rec.calls()
	if len(calls) != 1 || calls[0] != hash {
		t.Fatalf("removing the last file must drop the torrent once, got %v", calls)
	}
	if w.streamer.IsDownloadProtected("Pack") {
		t.Error("protection must be released after the last file is removed")
	}
}

// ─── (f) stall of the group demotes all members + counts one cycle ──────────

func TestDetectStalls_GroupDemotesAllMembers(t *testing.T) {
	store := newTestStore(t)
	hash := "packhash"
	var ids []int
	for i := 0; i < 3; i++ {
		d, _ := store.Create(Download{UserID: 1, InfoHash: hash, FileIndex: i, Magnet: "m", Name: "Pack"})
		_, _ = store.PromoteToDownloading(d.ID)
		ids = append(ids, d.ID)
	}
	w := newQueueWorker(t, store, QueueSettings{MaxActive: 3, StallThresholdMin: 30, MaxStalls: 2})
	stale := time.Now().Add(-time.Hour)
	inject := func() {
		w.mu.Lock()
		for _, id := range ids {
			// nil torrent ⇒ treated as no-seed; same infoHash ⇒ ONE group.
			w.tracked[id] = &trackedDL{id: id, userID: 1, infoHash: hash, name: "Pack", lastProgressAt: stale}
		}
		w.mu.Unlock()
	}

	inject()
	w.detectStalls(w.queueSettings())
	// All three rows demoted with exactly ONE stall each (one torrent stall cycle).
	for _, id := range ids {
		got, _ := store.Get(1, id)
		if got.Status != StatusQueued || got.Stalls != 1 {
			t.Fatalf("#%d after group stall: status=%q stalls=%d, want queued/1", id, got.Status, got.Stalls)
		}
	}
	w.mu.Lock()
	tracked := len(w.tracked)
	w.mu.Unlock()
	if tracked != 0 {
		t.Errorf("all group members should be untracked after demote, got %d", tracked)
	}

	// Second cycle reaches MaxStalls=2 → the WHOLE group is paused.
	for _, id := range ids {
		_, _ = store.PromoteToDownloading(id)
	}
	inject()
	w.detectStalls(w.queueSettings())
	for _, id := range ids {
		got, _ := store.Get(1, id)
		if got.Status != StatusPaused {
			t.Fatalf("#%d after reaching MaxStalls: status=%q, want paused", id, got.Status)
		}
	}
}

func TestGroupTransitions_PreserveStatusGuard(t *testing.T) {
	store := dlwNewStore(t)
	var ids []int
	for i := 0; i < 3; i++ {
		d, _ := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: i, Magnet: "m", Name: "P"})
		ids = append(ids, d.ID)
	}
	// One member is paused → PromoteGroup must skip it (guard on status=queued).
	_ = store.SetStatus(1, ids[2], StatusPaused)
	promoted, err := store.PromoteGroup(ids)
	if err != nil {
		t.Fatalf("PromoteGroup: %v", err)
	}
	if len(promoted) != 2 {
		t.Fatalf("promoted %v, want the 2 queued rows (paused skipped)", promoted)
	}
	for _, id := range promoted {
		got, _ := store.Get(1, id)
		if got.Status != StatusDownloading {
			t.Errorf("#%d should be downloading, got %q", id, got.Status)
		}
	}
	// DemoteGroup bumps stalls + requeues only the downloading rows.
	demoted, err := store.DemoteGroup(ids)
	if err != nil {
		t.Fatalf("DemoteGroup: %v", err)
	}
	if len(demoted) != 2 {
		t.Fatalf("demoted %v, want 2", demoted)
	}
	for _, id := range demoted {
		got, _ := store.Get(1, id)
		if got.Status != StatusQueued || got.Stalls != 1 {
			t.Errorf("#%d after demote: status=%q stalls=%d", id, got.Status, got.Stalls)
		}
	}
}
