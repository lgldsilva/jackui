package handlers

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// Destination-collision policy for promote (hardening fix): a FRESH promote
// must never clobber an existing destination item (os.Rename overwrites it) and
// must never treat an unrelated same-size decoy as "already copied" — the copy
// helper would then DELETE the source. Only a persisted pending intent for the
// exact src→dst pair (written before an interrupted copy started) turns an
// existing destination into a legitimate RESUME.

// A fresh promote onto an existing (unrelated, different-size) destination must
// be rejected — previously the plan was built and the rename clobbered the decoy.
func TestPromotePreparePlan_FreshMoveDstExistsErrors(t *testing.T) {
	store := hgAStore(t)
	srcDir := t.TempDir()
	shared := t.TempDir()
	d := hgACompletedDownload(t, store, srcDir, "movie.mkv")
	dst := filepath.Join(shared, "movie.mkv")
	decoy := "unrelated-longer-content"
	if err := os.WriteFile(dst, []byte(decoy), 0o644); err != nil {
		t.Fatal(err)
	}

	o := &promoteOpts{store: store, s: streamer.NewForTesting(), sharedDir: shared, userID: 0, id: d.ID}
	plan, err := promotePreparePlan(o)
	if err == nil {
		t.Fatalf("expected a collision error, got plan %+v (dst would be clobbered)", plan)
	}
	if !strings.Contains(err.Error(), "destination already exists") {
		t.Fatalf("err = %v, want it to contain %q", err, "destination already exists")
	}
	if plan != nil {
		t.Fatal("plan must be nil on a destination collision")
	}
	// Neither side may be touched by the rejected move.
	if _, serr := os.Stat(d.FilePath); serr != nil {
		t.Errorf("source vanished on a rejected move: %v", serr)
	}
	if body, rerr := os.ReadFile(dst); rerr != nil || string(body) != decoy {
		t.Errorf("existing destination item was modified: %q (%v)", body, rerr)
	}
}

// Ambiguity is NOT resolved by size: an existing destination with the SAME size
// as the source must still error on a fresh move — "same size" cannot
// distinguish a finished copy from a decoy, and guessing wrong deletes the source.
func TestPromotePreparePlan_FreshMoveDstSameSizeStillErrors(t *testing.T) {
	store := hgAStore(t)
	srcDir := t.TempDir()
	shared := t.TempDir()
	d := hgACompletedDownload(t, store, srcDir, "movie.mkv") // src content is 7 bytes
	dst := filepath.Join(shared, "movie.mkv")
	if err := os.WriteFile(dst, []byte("decoy42"), 0o644); err != nil { // also 7 bytes
		t.Fatal(err)
	}

	o := &promoteOpts{store: store, s: streamer.NewForTesting(), sharedDir: shared, userID: 0, id: d.ID}
	if plan, err := promotePreparePlan(o); err == nil {
		t.Fatalf("same-size decoy must still error on a fresh move, got plan %+v", plan)
	} else if !strings.Contains(err.Error(), "destination already exists") {
		t.Fatalf("err = %v, want it to contain %q", err, "destination already exists")
	}
	if _, serr := os.Stat(d.FilePath); serr != nil {
		t.Errorf("source vanished on a rejected move: %v", serr)
	}
}

// Resume contract: when a pending promote intent exists for the exact
// src→dst pair, an existing destination is the artifact of THAT interrupted
// copy — planning must proceed so the resume-aware copy finishes the job.
func TestPromotePreparePlan_PendingIntentAllowsResume(t *testing.T) {
	store := hgAStore(t)
	srcDir := t.TempDir()
	shared := t.TempDir()
	d := hgACompletedDownload(t, store, srcDir, "movie.mkv")
	dst := filepath.Join(shared, "movie.mkv")
	// Partial artifact of the interrupted copy this test resumes.
	if err := os.WriteFile(dst, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	pend, err := transfer.OpenStore(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"downloadID":` + strconv.Itoa(d.ID) + `,"userID":0}`
	if _, err := pend.Add(transfer.Pending{Kind: "promote", Src: d.FilePath, Dst: dst, Payload: payload}); err != nil {
		t.Fatal(err)
	}

	o := &promoteOpts{store: store, s: streamer.NewForTesting(), sharedDir: shared, userID: 0, id: d.ID, pending: pend}
	plan, err := promotePreparePlan(o)
	if err != nil {
		t.Fatalf("resume with a pending intent must be allowed, got: %v", err)
	}
	if plan == nil {
		t.Fatal("plan must be non-nil for a resume")
	}
	if plan.dst != dst || plan.src != d.FilePath {
		t.Fatalf("plan = (%s → %s), want (%s → %s)", plan.src, plan.dst, d.FilePath, dst)
	}
}

// The batch endpoint surfaces the collision per-item in `failed` (the response
// shape other items rely on) instead of silently overwriting the destination.
func TestPromoteBatchItems_FreshCollisionReportedAsFailed(t *testing.T) {
	store := hgAStore(t)
	srcDir := t.TempDir()
	shared := t.TempDir()
	d := hgACompletedDownload(t, store, srcDir, "batch.mkv")
	if err := os.WriteFile(filepath.Join(shared, "batch.mkv"), []byte("already-here-longer"), 0o644); err != nil {
		t.Fatal(err)
	}

	o := &promoteOpts{store: store, s: streamer.NewForTesting(), sharedDir: shared, userID: 0, id: d.ID}
	promoted, failed := promoteBatchItems(o, &promoteReq{IDs: []int{d.ID}}, nil)
	if len(promoted) != 0 {
		t.Fatalf("promoted = %+v, want none (collision)", promoted)
	}
	if len(failed) != 1 {
		t.Fatalf("failed = %v, want exactly one entry", failed)
	}
	if msg, _ := failed[0]["error"].(string); !strings.Contains(msg, "destination already exists") {
		t.Fatalf("failure message = %q, want it to contain %q", msg, "destination already exists")
	}
}
