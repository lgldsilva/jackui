package downloads

import (
	"context"
	"errors"
	"testing"
)

func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// A done context must stop every store call before it reaches Postgres, and a
// cancelled call must change nothing (no half-applied status transition).
func TestStoreCallsHonourCancelledContext(t *testing.T) {
	s := newTestStore(t)
	live := context.Background()
	d, err := s.Create(live, Download{UserID: 1, InfoHash: "aa", FileIndex: 0, Magnet: "m", Name: "n", Status: StatusQueued})
	if err != nil {
		t.Fatal(err)
	}
	ctx := cancelledCtx()
	calls := map[string]func() error{
		"Create": func() error {
			_, err := s.Create(ctx, Download{UserID: 1, InfoHash: "bb", FileIndex: 0, Magnet: "m"})
			return err
		},
		"BatchCreate": func() error {
			_, err := s.BatchCreate(ctx, []Download{{UserID: 1, InfoHash: "cc", FileIndex: 0, Magnet: "m"}})
			return err
		},
		"EnqueueMagnet": func() error { return s.EnqueueMagnet(ctx, 1, "dd", "n", "m", "") },
		"CreateLinked": func() error {
			_, err := s.CreateLinked(ctx, Download{UserID: 1, InfoHash: "ee", FileIndex: 1}, "/x", 1)
			return err
		},
		"Get":                  func() error { _, err := s.Get(ctx, 1, d.ID); return err },
		"GetByKey":             func() error { _, err := s.GetByKey(ctx, 1, "aa", 0); return err },
		"List":                 func() error { _, err := s.List(ctx, 1); return err },
		"ListFiltered":         func() error { _, err := s.ListFiltered(ctx, ListFilter{UserID: 1}); return err },
		"ListFilteredAll":      func() error { _, err := s.ListFilteredAll(ctx, ListFilter{}); return err },
		"ListAll":              func() error { _, err := s.ListAll(ctx); return err },
		"ListActive":           func() error { _, err := s.ListActive(ctx); return err },
		"ListMoving":           func() error { _, err := s.ListMoving(ctx); return err },
		"ListSchedulable":      func() error { _, err := s.ListSchedulable(ctx); return err },
		"WantedRowsByHash":     func() error { _, err := s.WantedRowsByHash(ctx, 1, "aa"); return err },
		"DistinctUsers":        func() error { _, err := s.DistinctUsers(ctx); return err },
		"DistinctTrackers":     func() error { _, err := s.DistinctTrackers(ctx, 1); return err },
		"DistinctCategories":   func() error { _, err := s.DistinctCategories(ctx, 1); return err },
		"HashOwnerIDs":         func() error { _, err := s.HashOwnerIDs(ctx, "aa"); return err },
		"HashSetForUser":       func() error { _, err := s.HashSetForUser(ctx, 1, false); return err },
		"UserStats":            func() error { _, _, _, err := s.UserStats(ctx, 1); return err },
		"FindByPathPrefix":     func() error { _, err := s.FindByPathPrefix(ctx, "/x"); return err },
		"CompletedBySize":      func() error { _, err := s.CompletedBySize(ctx, 1, 1); return err },
		"RefCountPath":         func() error { _, err := s.RefCountPath(ctx, "/x"); return err },
		"GetCompletedPath":     func() error { _, err := s.GetCompletedPath(ctx, "aa", 0, 1); return err },
		"GetCompletedPathRel":  func() error { _, err := s.GetCompletedPathRel(ctx, "aa", 0, "r", 1); return err },
		"GetCompletedPathsRel": func() error { _, err := s.GetCompletedPathsRel(ctx, "aa", []string{"r"}, 1); return err },
		"SetStatus":            func() error { return s.SetStatus(ctx, 1, d.ID, StatusPaused) },
		"SetStatusForUser":     func() error { _, err := s.SetStatusForUser(ctx, 1, StatusPaused); return err },
		"SetStatusByIDs":       func() error { _, err := s.SetStatusByIDs(ctx, 1, []int{d.ID}, StatusPaused); return err },
		"SetError":             func() error { return s.SetError(ctx, 1, d.ID, "x") },
		"SetActiveMagnet":      func() error { return s.SetActiveMagnet(ctx, 1, d.ID, "m2") },
		"SetFilePath":          func() error { return s.SetFilePath(ctx, 1, d.ID, "/p") },
		"UpdateName":           func() error { return s.UpdateName(ctx, 1, d.ID, "new") },
		"SetCategory":          func() error { return s.SetCategory(ctx, 1, d.ID, "cat") },
		"UpdateMetadata":       func() error { return s.UpdateMetadata(ctx, 1, d.ID, "n2", "/p", 5) },
		"SetCompletionDest":    func() error { return s.SetCompletionDest(ctx, 1, d.ID, "/d") },
		"UpdateProgress":       func() error { return s.UpdateProgress(ctx, 1, d.ID, 9) },
		"UpdateProgressBatch":  func() error { return s.UpdateProgressBatch(ctx, []ProgressUpdate{{ID: d.ID, UserID: 1, Bytes: 9}}) },
		"StopSeed":             func() error { return s.StopSeed(ctx, 1, d.ID) },
		"ResumeSeed":           func() error { return s.ResumeSeed(ctx, 1, d.ID) },
		"StopSeedByInfoHash":   func() error { return s.StopSeedByInfoHash(ctx, 1, "aa") },
		"SetFileIndex":         func() error { return s.SetFileIndex(ctx, 1, d.ID, 3) },
		"SetPriority":          func() error { return s.SetPriority(ctx, 1, d.ID, "high") },
		"PromoteGroup":         func() error { _, err := s.PromoteGroup(ctx, []int{d.ID}); return err },
		"PreemptGroup":         func() error { _, err := s.PreemptGroup(ctx, []int{d.ID}); return err },
		"DemoteGroup":          func() error { _, err := s.DemoteGroup(ctx, []int{d.ID}); return err },
		"MoveGroup":            func() error { _, err := s.MoveGroup(ctx, []int{d.ID}); return err },
		"CompleteGroup":        func() error { _, err := s.CompleteGroup(ctx, []int{d.ID}); return err },
		"PromoteToDownloading": func() error { _, err := s.PromoteToDownloading(ctx, d.ID); return err },
		"PreemptToQueued":      func() error { _, err := s.PreemptToQueued(ctx, d.ID); return err },
		"DemoteToQueued":       func() error { _, _, err := s.DemoteToQueued(ctx, d.ID); return err },
		"Requeue":              func() error { return s.Requeue(ctx, 1, d.ID) },
		"RequeueForUser":       func() error { _, err := s.RequeueForUser(ctx, 1); return err },
		"RequeueByIDs":         func() error { _, err := s.RequeueByIDs(ctx, 1, []int{d.ID}); return err },
		"EnsureSource": func() error {
			return s.EnsureSource(ctx, Source{DownloadID: d.ID, InfoHash: "zz", Magnet: "m"}, SourceCandidate)
		},
		"ListSources":  func() error { _, err := s.ListSources(ctx, d.ID); return err },
		"HasSources":   func() error { _, err := s.HasSources(ctx, d.ID); return err },
		"NextSource":   func() error { _, err := s.NextSource(ctx, d.ID, 1); return err },
		"Delete":       func() error { return s.Delete(ctx, 1, d.ID) },
		"DeleteScoped": func() error { _, err := s.DeleteScoped(ctx, 1, d.ID, true); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want context.Canceled", name, err)
		}
	}
	got, err := s.Get(live, 1, d.ID)
	if err != nil {
		t.Fatalf("row gone after cancelled calls: %v", err)
	}
	if got.Status != StatusQueued || got.Name != "n" || got.FilePath != "" || got.BytesDownloaded != 0 {
		t.Fatalf("a cancelled call changed the row: %+v", got)
	}
	if all, _ := s.ListAll(live); len(all) != 1 {
		t.Fatalf("cancelled calls created rows: %d", len(all))
	}
}

// A read that fails must surface as an error, never as "nothing there" that a
// caller then acts on (DeleteScoped's lookup precedes the DELETE and its caller
// tears the torrent down from the returned row).
func TestDeleteScoped_CancelledLookupIsAnErrorNotAlreadyGone(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(context.Background(), Download{UserID: 1, InfoHash: "aa", FileIndex: 0, Magnet: "m"})
	row, err := s.DeleteScoped(cancelledCtx(), 1, d.ID, false)
	if row != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteScoped = %v, %v; want nil row and context.Canceled", row, err)
	}
	if _, err := s.Get(context.Background(), 1, d.ID); err != nil {
		t.Fatalf("row was deleted: %v", err)
	}
}

// The worker's reads stop with Stop(), but its status writes must not: a
// transition in flight when Stop() lands is applied whole, never dropped.
func TestWorkerWriteCtxSurvivesStop(t *testing.T) {
	w := NewWorker(WorkerConfig{Store: newTestStore(t)})
	w.Stop()
	if w.readCtx().Err() == nil {
		t.Error("readCtx must be cancelled by Stop()")
	}
	if err := w.writeCtx().Err(); err != nil {
		t.Errorf("writeCtx cancelled by Stop(): %v", err)
	}
	d, err := w.store.Create(w.writeCtx(), Download{UserID: 1, InfoHash: "aa", Magnet: "m"})
	if err != nil {
		t.Fatalf("a write after Stop() was dropped: %v", err)
	}
	if err := w.store.SetStatus(w.writeCtx(), 1, d.ID, StatusPaused); err != nil {
		t.Fatalf("status write after Stop(): %v", err)
	}
}

func TestWorkerCtxHelpersAreNilSafe(t *testing.T) {
	w := &Worker{} // built without NewWorker, as several tests do
	if w.readCtx() == nil || w.writeCtx() == nil {
		t.Fatal("ctx helpers must fall back to Background")
	}
}
