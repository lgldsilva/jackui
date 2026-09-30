package handlers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// A completed row with no file_path has nothing on disk to promote — the plan
// must fail up front instead of stat'ing "".
func Test_promotePrepare_EmptyFilePath(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	d, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "nofile"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.SetStatus(0, d.ID, downloads.StatusCompleted)

	plan, err := promotePreparePlan(&promoteOpts{store: store, s: s, sharedDir: t.TempDir(), userID: 0, id: d.ID})
	if err == nil || !strings.Contains(err.Error(), "file_path empty") {
		t.Fatalf("err = %v, want file_path-empty", err)
	}
	if plan != nil {
		t.Errorf("plan should be nil on error, got %+v", plan)
	}
}

// When the file already sits at the destination the plan is a no-op (nil, nil):
// the handler reports success without scheduling a copy.
func Test_promotePrepare_AlreadyInPlace(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	shared := t.TempDir()
	d := hgACompletedDownload(t, store, shared, "movie.mkv") // file_path already inside sharedDir

	plan, err := promotePreparePlan(&promoteOpts{store: store, s: s, sharedDir: shared, userID: 0, id: d.ID})
	if err != nil {
		t.Fatalf("promotePreparePlan: %v", err)
	}
	if plan != nil {
		t.Errorf("expected nil plan for src == dst, got %+v", plan)
	}
}

// failingPlan builds a plan whose destination parent is a regular FILE, so both
// the rename and the copy fallback fail — the move error path.
func failingPlan(t *testing.T) *promotePlan {
	t.Helper()
	src := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &promotePlan{
		d:       &downloads.Download{ID: 7, Name: "movie.mkv"},
		src:     src,
		dst:     filepath.Join(blocker, "movie.mkv"),
		srcInfo: info,
		files:   1,
		bytes:   info.Size(),
	}
}

func Test_runPromotePlan_MoveError(t *testing.T) {
	err := runPromotePlan(&promoteOpts{}, failingPlan(t), nil)
	if err == nil || !strings.HasPrefix(err.Error(), "move file: ") {
		t.Fatalf("err = %v, want move-file error", err)
	}
}

func waitTrackerIdle(t *testing.T, tr *transfer.Tracker) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !tr.WaitIdle(ctx) {
		t.Fatal("tracker did not drain")
	}
}

func onlyJob(t *testing.T, tr *transfer.Tracker) transfer.Snapshot {
	t.Helper()
	jobs := tr.List(0, true)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1 (%+v)", len(jobs), jobs)
	}
	return jobs[0]
}

// Parallel mode: each plan is its own job and a failed copy marks that job
// failed (the user sees the error in the Transfers dock, not a silent success).
func Test_submitPromotePlans_ParallelFailure(t *testing.T) {
	tr := transfer.New()
	o := &promoteOpts{concMode: transferModeParallel}
	submitPromotePlans(o, tr, []*promotePlan{failingPlan(t)})
	waitTrackerIdle(t, tr)

	if j := onlyJob(t, tr); j.Status != transfer.StatusFailed {
		t.Errorf("job status = %q, want failed", j.Status)
	}
}

// Serial mode (HDD): several plans fold into ONE job labelled "N items"; the
// first error is what fails the job, but every plan still gets attempted.
func Test_submitPromotePlans_SerialFailure(t *testing.T) {
	tr := transfer.New()
	o := &promoteOpts{concMode: transferModeSerial}
	submitPromotePlans(o, tr, []*promotePlan{failingPlan(t), failingPlan(t)})
	waitTrackerIdle(t, tr)

	j := onlyJob(t, tr)
	if j.Status != transfer.StatusFailed {
		t.Errorf("job status = %q, want failed", j.Status)
	}
	if j.Label != "2 items" {
		t.Errorf("label = %q, want %q", j.Label, "2 items")
	}
}

func Test_submitPromotePlans_Empty(t *testing.T) {
	tr := transfer.New()
	submitPromotePlans(&promoteOpts{}, tr, nil)
	if n := len(tr.List(0, true)); n != 0 {
		t.Errorf("no plans should submit no jobs, got %d", n)
	}
}
