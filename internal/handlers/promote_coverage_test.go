package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// Coverage for promote branches surfaced by the i18n sweep (error paths and
// the already-at-destination short-circuit).

// When the row already points at the computed destination the planner returns
// a nil plan ("already in place") and promoteBatchItems counts it as promoted
// without submitting any copy.
func Test_promoteBatch_AlreadyAtDestination(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	shared := t.TempDir()
	src := filepath.Join(shared, "inplace.mkv")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "inplace.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetFilePath(0, d.ID, src); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus(0, d.ID, downloads.StatusCompleted); err != nil {
		t.Fatal(err)
	}

	o := &promoteOpts{store: store, s: s, sharedDir: shared, userID: 0, id: d.ID}
	plan, err := promotePreparePlan(o)
	if err != nil {
		t.Fatalf("promotePreparePlan: %v", err)
	}
	if plan != nil {
		t.Fatalf("plan = %+v, want nil (src == dst)", plan)
	}

	promoted, failed := promoteBatchItems(o, &promoteReq{IDs: []int{d.ID}}, nil)
	if len(failed) != 0 {
		t.Fatalf("failed = %v, want none", failed)
	}
	if len(promoted) != 1 || promoted[0].ID != d.ID {
		t.Fatalf("promoted = %+v, want [%d]", promoted, d.ID)
	}
}

// runPromotePlan surfaces a move failure as "move file: ..." when the source
// vanishes between planning and execution.
func Test_runPromotePlan_MoveFailure(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	srcDir := t.TempDir()
	shared := t.TempDir()
	d := hgACompletedDownload(t, store, srcDir, "vanish.mkv")

	o := &promoteOpts{store: store, s: s, sharedDir: shared, userID: 0, id: d.ID}
	plan, err := promotePreparePlan(o)
	if err != nil {
		t.Fatalf("promotePreparePlan: %v", err)
	}
	if err := os.Remove(plan.src); err != nil {
		t.Fatal(err)
	}
	err = runPromotePlan(o, plan, nil)
	if err == nil || !strings.Contains(err.Error(), "move file:") {
		t.Fatalf("err = %v, want move file failure", err)
	}
}

// waitForSettled polls the tracker until no job is queued/running anymore.
func waitForSettled(t *testing.T, tr *transfer.Tracker) []transfer.Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		list := tr.List(0, true)
		settled := true
		for _, s := range list {
			if s.Status == transfer.StatusQueued || s.Status == transfer.StatusRunning {
				settled = false
				break
			}
		}
		if settled {
			return list
		}
		if time.Now().After(deadline) {
			t.Fatalf("jobs did not settle: %+v", list)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The parallel path reports each failed item's log line and fails its job.
func Test_submitPromotePlans_ParallelItemFailure(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	srcDir := t.TempDir()
	shared := t.TempDir()
	d := hgACompletedDownload(t, store, srcDir, "par.mkv")

	o := &promoteOpts{store: store, s: s, sharedDir: shared, userID: 0, id: d.ID, concMode: "parallel"}
	plan, err := promotePreparePlan(o)
	if err != nil {
		t.Fatalf("promotePreparePlan: %v", err)
	}
	if err := os.Remove(plan.src); err != nil {
		t.Fatal(err)
	}

	tr := transfer.New()
	submitPromotePlans(o, tr, []*promotePlan{plan})
	list := waitForSettled(t, tr)
	if len(list) != 1 || list[0].Status != transfer.StatusFailed {
		t.Fatalf("snapshots = %+v, want one failed job", list)
	}
}

// completedDownloadWithHash is hgACompletedDownload with an explicit info
// hash: the store dedupes rows by (user, info_hash), so two rows in one test
// MUST NOT share one.
func completedDownloadWithHash(t *testing.T, store *downloads.Store, srcDir, fileName, hashHex string) *downloads.Download {
	t.Helper()
	src := filepath.Join(srcDir, fileName)
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := store.Create(downloads.Download{
		InfoHash: hashHex,
		Name:     fileName,
		Magnet:   MagnetPrefix + hashHex,
		FilePath: src,
		FileSize: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetFilePath(0, d.ID, src); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus(0, d.ID, downloads.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(0, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The serial path labels a multi-item submission as "N items" and logs each
// item failure while still completing the healthy ones.
func Test_submitPromoteSerial_MultiItemLabelAndFailure(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	srcDir := t.TempDir()
	shared := t.TempDir()
	dOk := completedDownloadWithHash(t, store, srcDir, "ok.mkv", hgAValidHash)
	// Flip one hash nibble: a second row must not alias the first.
	badHash := strings.ToUpper(hgAValidHash[:len(hgAValidHash)-1]) + "e"
	dBad := completedDownloadWithHash(t, store, srcDir, "bad.mkv", badHash)

	o := &promoteOpts{store: store, s: s, sharedDir: shared, userID: 0, id: dOk.ID, concMode: "serial"}
	var plans []*promotePlan
	for _, id := range []int{dOk.ID, dBad.ID} {
		o.id = id
		plan, err := promotePreparePlan(o)
		if err != nil {
			t.Fatalf("promotePreparePlan #%d: %v", id, err)
		}
		plans = append(plans, plan)
	}
	if err := os.Remove(plans[1].src); err != nil {
		t.Fatal(err)
	}

	tr := transfer.New()
	submitPromoteSerial(o, tr, plans)
	list := waitForSettled(t, tr)
	// The serial submission is ONE job: the healthy item moves first, then the
	// failure of the second marks the whole job failed (firstErr semantics).
	if len(list) != 1 {
		t.Fatalf("snapshots = %+v, want a single serial job", list)
	}
	if list[0].Status != transfer.StatusFailed {
		t.Fatalf("status = %q, want failed (bad.mkv)", list[0].Status)
	}
	if list[0].Label != "2 items" {
		t.Fatalf("label = %q, want \"2 items\" (multi-item serial submission)", list[0].Label)
	}
	// The healthy item landed at the destination and its row was re-pointed.
	if _, err := os.Stat(filepath.Join(shared, "ok.mkv")); err != nil {
		t.Errorf("healthy item not moved: %v", err)
	}
	updated, _ := store.Get(0, dOk.ID)
	if updated.FilePath != filepath.Join(shared, "ok.mkv") {
		t.Errorf("FilePath = %q, want the shared-dir destination", updated.FilePath)
	}
}
