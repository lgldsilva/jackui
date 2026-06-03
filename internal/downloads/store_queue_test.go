package downloads

import "testing"

func TestCreate_DefaultsToNormalPriorityAndQueued(t *testing.T) {
	s := newTestStore(t)
	d, err := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if d.Status != StatusQueued {
		t.Errorf("status: want queued, got %q", d.Status)
	}
	if d.Priority != PriorityNormal {
		t.Errorf("priority: want normal, got %q", d.Priority)
	}
	if d.QueuedSince == nil {
		t.Error("queued_since should be set on create")
	}
}

func TestCreate_HonorsExplicitPriority(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A", Priority: PriorityHigh})
	if d.Priority != PriorityHigh {
		t.Errorf("priority: want high, got %q", d.Priority)
	}
	// Invalid priority falls back to normal.
	d2, _ := s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "B", Priority: "bogus"})
	if d2.Priority != PriorityNormal {
		t.Errorf("invalid priority should fall back to normal, got %q", d2.Priority)
	}
}

func TestSetPriority(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A"})
	if err := s.SetPriority(1, d.ID, PriorityHigh); err != nil {
		t.Fatalf("SetPriority: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.Priority != PriorityHigh {
		t.Errorf("want high, got %q", got.Priority)
	}
	if err := s.SetPriority(1, d.ID, "bogus"); err == nil {
		t.Error("expected error for invalid priority")
	}
}

func TestListSchedulable_OnlyDownloadingAndQueued(t *testing.T) {
	s := newTestStore(t)
	q, _ := s.Create(Download{UserID: 1, InfoHash: "q", FileIndex: 0, Magnet: "m", Name: "Q"})
	dl, _ := s.Create(Download{UserID: 1, InfoHash: "d", FileIndex: 0, Magnet: "m", Name: "D"})
	paused, _ := s.Create(Download{UserID: 1, InfoHash: "p", FileIndex: 0, Magnet: "m", Name: "P"})
	done, _ := s.Create(Download{UserID: 1, InfoHash: "c", FileIndex: 0, Magnet: "m", Name: "C"})
	_, _ = s.PromoteToDownloading(dl.ID)
	_ = s.SetStatus(1, paused.ID, StatusPaused)
	_ = s.SetStatus(1, done.ID, StatusCompleted)

	sched, err := s.ListSchedulable()
	if err != nil {
		t.Fatalf("ListSchedulable: %v", err)
	}
	got := map[int]string{}
	for _, d := range sched {
		got[d.ID] = d.Status
	}
	if len(got) != 2 || got[q.ID] != StatusQueued || got[dl.ID] != StatusDownloading {
		t.Fatalf("expected only queued #%d + downloading #%d, got %+v", q.ID, dl.ID, got)
	}
}

func TestPromoteToDownloading_GuardedOnStatus(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A"})
	ok, err := s.PromoteToDownloading(d.ID)
	if err != nil || !ok {
		t.Fatalf("promote: ok=%v err=%v", ok, err)
	}
	got, _ := s.Get(1, d.ID)
	if got.Status != StatusDownloading || got.StartedAt == nil {
		t.Fatalf("expected downloading with started_at, got %+v", got)
	}
	// Second promote is a no-op (already downloading, not queued).
	ok, _ = s.PromoteToDownloading(d.ID)
	if ok {
		t.Error("expected no-op promote on already-downloading row")
	}
}

func TestDemoteToQueued_ResetsQueuedSinceAndBumpsStalls(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A"})
	_, _ = s.PromoteToDownloading(d.ID)

	stalls, demoted, err := s.DemoteToQueued(d.ID)
	if err != nil || !demoted {
		t.Fatalf("demote: demoted=%v err=%v", demoted, err)
	}
	if stalls != 1 {
		t.Errorf("expected stalls=1, got %d", stalls)
	}
	got, _ := s.Get(1, d.ID)
	if got.Status != StatusQueued {
		t.Errorf("expected queued, got %q", got.Status)
	}
	if got.QueuedSince == nil {
		t.Error("queued_since should be reset on demote")
	}
	// Demote of a queued row is a no-op (guarded on downloading).
	_, demoted, _ = s.DemoteToQueued(d.ID)
	if demoted {
		t.Error("expected no-op demote on queued row")
	}
}

func TestRequeue_FromPaused(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A"})
	_ = s.SetError(1, d.ID, "boom") // → failed
	if err := s.Requeue(1, d.ID); err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.Status != StatusQueued || got.Error != "" {
		t.Fatalf("expected queued with cleared error, got status=%q err=%q", got.Status, got.Error)
	}
}

func TestRequeue_NoOpOnDownloading(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A"})
	_, _ = s.PromoteToDownloading(d.ID)
	if err := s.Requeue(1, d.ID); err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	got, _ := s.Get(1, d.ID)
	if got.Status != StatusDownloading {
		t.Errorf("requeue must not touch a downloading row, got %q", got.Status)
	}
}

func TestRequeueForUser_OnlyPaused(t *testing.T) {
	s := newTestStore(t)
	paused, _ := s.Create(Download{UserID: 1, InfoHash: "p", FileIndex: 0, Magnet: "m", Name: "P"})
	dl, _ := s.Create(Download{UserID: 1, InfoHash: "d", FileIndex: 0, Magnet: "m", Name: "D"})
	done, _ := s.Create(Download{UserID: 1, InfoHash: "c", FileIndex: 0, Magnet: "m", Name: "C"})
	_ = s.SetStatus(1, paused.ID, StatusPaused)
	_, _ = s.PromoteToDownloading(dl.ID)
	_ = s.SetStatus(1, done.ID, StatusCompleted)

	n, err := s.RequeueForUser(1)
	if err != nil {
		t.Fatalf("RequeueForUser: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 requeued (the paused one), got %d", n)
	}
	got, _ := s.Get(1, paused.ID)
	if got.Status != StatusQueued {
		t.Errorf("paused → queued expected, got %q", got.Status)
	}
}

func TestRequeueByIDs(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.Create(Download{UserID: 1, InfoHash: "a", FileIndex: 0, Magnet: "m", Name: "A"})
	b, _ := s.Create(Download{UserID: 1, InfoHash: "b", FileIndex: 0, Magnet: "m", Name: "B"})
	_ = s.SetStatus(1, a.ID, StatusPaused)
	_ = s.SetStatus(1, b.ID, StatusPaused)

	n, err := s.RequeueByIDs(1, []int{a.ID, b.ID})
	if err != nil {
		t.Fatalf("RequeueByIDs: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 requeued, got %d", n)
	}
	if got, _ := s.Get(1, a.ID); got.Status != StatusQueued {
		t.Errorf("a should be queued, got %q", got.Status)
	}
	// Empty list is a safe no-op.
	if n, _ := s.RequeueByIDs(1, nil); n != 0 {
		t.Errorf("expected 0 for empty ids, got %d", n)
	}
}
