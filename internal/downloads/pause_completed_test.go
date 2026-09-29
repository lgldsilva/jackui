package downloads

import (
	"testing"
)

// Pausing an ALREADY COMPLETED download used to orphan the row: the status
// became `paused`, but Requeue (store_groups.go) refuses to leave `completed`,
// so the resume was a no-op and the row stayed stuck in `paused` forever. In
// the UI the item lost its completed-only actions (Promote / Stop and remove /
// Open in folder), which are conditioned on status==='completed' — hence the
// report "it won't leave the list and the option to delete the torrent keeping
// the files never shows up".
//
// SetStatusForUser (pause-all) ALWAYS excluded terminals; the single/batch
// path was the one missing the guard. Here it becomes a store rule.
func TestSetStatusPausedIgnoresCompleted(t *testing.T) {
	s := newTestStore(t)
	d := mustCreate(t, s, 1, "aaa", 0)
	if err := s.SetStatus(1, d.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}

	if err := s.SetStatus(1, d.ID, StatusPaused); err != nil {
		t.Fatalf("SetStatus paused: %v", err)
	}

	got, err := s.Get(1, d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusCompleted {
		t.Errorf("status=%q, want %q — pausing a completed row must be a no-op", got.Status, StatusCompleted)
	}
}

// Same guard for `failed`: the card offers "Try again" (resume), not pause.
// Pausing a failed row would only hide it from the errors tab.
func TestSetStatusPausedIgnoresFailed(t *testing.T) {
	s := newTestStore(t)
	d := mustCreate(t, s, 1, "bbb", 0)
	if err := s.SetStatus(1, d.ID, StatusFailed); err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}

	if err := s.SetStatus(1, d.ID, StatusPaused); err != nil {
		t.Fatalf("SetStatus paused: %v", err)
	}

	got, _ := s.Get(1, d.ID)
	if got.Status != StatusFailed {
		t.Errorf("status=%q, want %q — pausing a failed row must be a no-op", got.Status, StatusFailed)
	}
}

// The guard only applies to pause: completed→downloading remains free (that's
// the re-download / re-enqueue path, and SetStatus(completed) re-enables
// auto-seed by clearing seed_stopped_at).
func TestSetStatusCompletedToDownloadingStillAllowed(t *testing.T) {
	s := newTestStore(t)
	d := mustCreate(t, s, 1, "ccc", 0)
	if err := s.SetStatus(1, d.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}

	if err := s.SetStatus(1, d.ID, StatusDownloading); err != nil {
		t.Fatalf("SetStatus downloading: %v", err)
	}

	got, _ := s.Get(1, d.ID)
	if got.Status != StatusDownloading {
		t.Errorf("status=%q, want %q — re-download must not be blocked", got.Status, StatusDownloading)
	}
}

// The batch (PATCH /downloads/batch/pause) uses SetStatusByIDs — same guard, and
// `affected` must reflect only the rows actually paused so the frontend doesn't
// announce success over a no-op.
func TestSetStatusByIDsPausedSkipsTerminalRows(t *testing.T) {
	s := newTestStore(t)
	active := mustCreate(t, s, 1, "ddd", 0)
	completed := mustCreate(t, s, 1, "ddd", 1)
	failed := mustCreate(t, s, 1, "ddd", 2)
	if err := s.SetStatus(1, active.ID, StatusDownloading); err != nil {
		t.Fatalf("SetStatus downloading: %v", err)
	}
	if err := s.SetStatus(1, completed.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}
	if err := s.SetStatus(1, failed.ID, StatusFailed); err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}

	n, err := s.SetStatusByIDs(1, []int{active.ID, completed.ID, failed.ID}, StatusPaused)
	if err != nil {
		t.Fatalf("SetStatusByIDs: %v", err)
	}
	if n != 1 {
		t.Errorf("affected=%d, want 1 — only the active row can be paused", n)
	}

	gotCompleted, _ := s.Get(1, completed.ID)
	if gotCompleted.Status != StatusCompleted {
		t.Errorf("completed became %q on batch pause", gotCompleted.Status)
	}
	gotFailed, _ := s.Get(1, failed.ID)
	if gotFailed.Status != StatusFailed {
		t.Errorf("failed became %q on batch pause", gotFailed.Status)
	}
	gotActive, _ := s.Get(1, active.ID)
	if gotActive.Status != StatusPaused {
		t.Errorf("active row was not paused: %q", gotActive.Status)
	}
}

// SetStatusByIDs with other statuses (e.g. the worker demoting to queued) does
// not inherit the guard — it is pause-specific.
func TestSetStatusByIDsNonPausedUnaffectedByGuard(t *testing.T) {
	s := newTestStore(t)
	completed := mustCreate(t, s, 1, "eee", 0)
	if err := s.SetStatus(1, completed.ID, StatusCompleted); err != nil {
		t.Fatalf("SetStatus completed: %v", err)
	}

	n, err := s.SetStatusByIDs(1, []int{completed.ID}, StatusQueued)
	if err != nil {
		t.Fatalf("SetStatusByIDs: %v", err)
	}
	if n != 1 {
		t.Errorf("affected=%d, want 1 — re-queueing a completed row stays allowed", n)
	}
}

func mustCreate(t *testing.T, s *Store, userID int, infoHash string, fileIndex int) *Download {
	t.Helper()
	d, err := s.Create(Download{
		UserID: userID, InfoHash: infoHash, FileIndex: fileIndex,
		Magnet: "magnet:?xt=urn:btih:" + infoHash, Name: "x", FilePath: "x", FileSize: 10,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return d
}
