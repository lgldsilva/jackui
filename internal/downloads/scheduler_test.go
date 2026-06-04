package downloads

import (
	"testing"
	"time"
)

var schedNow = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

func qd(id int, status, priority string, queuedMinAgo, stalls int) Download {
	t := schedNow.Add(-time.Duration(queuedMinAgo) * time.Minute)
	return Download{ID: id, Status: status, Priority: priority, QueuedSince: &t, Stalls: stalls}
}

func TestSchedulePlan_RespectsMaxActive(t *testing.T) {
	items := []Download{
		qd(1, StatusQueued, PriorityNormal, 5, 0),
		qd(2, StatusQueued, PriorityNormal, 4, 0),
		qd(3, StatusQueued, PriorityNormal, 3, 0),
		qd(4, StatusQueued, PriorityNormal, 2, 0),
		qd(5, StatusQueued, PriorityNormal, 1, 0),
	}
	want := schedulePlan(items, SchedSettings{MaxActive: 3}, schedNow)
	if len(want) != 3 {
		t.Fatalf("expected 3 active, got %d (%v)", len(want), want)
	}
	// Oldest-waiting normals win (FIFO within tier): #1,#2,#3.
	for _, id := range []int{1, 2, 3} {
		if !want[id] {
			t.Errorf("expected #%d active", id)
		}
	}
}

func TestSchedulePlan_HighPriorityPromotedFirst(t *testing.T) {
	items := []Download{
		qd(1, StatusQueued, PriorityLow, 100, 0),  // waiting longest but low
		qd(2, StatusQueued, PriorityHigh, 1, 0),   // just queued but high
		qd(3, StatusQueued, PriorityNormal, 50, 0),
	}
	want := schedulePlan(items, SchedSettings{MaxActive: 1}, schedNow)
	if !want[2] || len(want) != 1 {
		t.Fatalf("expected only high-priority #2, got %v", want)
	}
}

func TestSchedulePlan_IncumbentKeepsSlotOverSamePriority(t *testing.T) {
	// A downloading normal must NOT be displaced by a queued normal (no thrash).
	items := []Download{
		qd(1, StatusDownloading, PriorityNormal, 0, 0),
		qd(2, StatusQueued, PriorityNormal, 999, 0), // waiting forever, same priority
	}
	want := schedulePlan(items, SchedSettings{MaxActive: 1, AgingStepMin: 0, AgingCap: 0}, schedNow)
	if !want[1] || want[2] {
		t.Fatalf("incumbent #1 should keep its slot, got %v", want)
	}
}

func TestSchedulePlan_HigherPriorityPreemptsIncumbent(t *testing.T) {
	// A queued HIGH preempts a downloading LOW when the limit is full.
	items := []Download{
		qd(1, StatusDownloading, PriorityLow, 0, 0),
		qd(2, StatusQueued, PriorityHigh, 0, 0),
	}
	want := schedulePlan(items, SchedSettings{MaxActive: 1}, schedNow)
	if !want[2] || want[1] {
		t.Fatalf("expected HIGH #2 to preempt LOW #1, got %v", want)
	}
}

func TestSchedulePlan_AgingLiftsStarvedLowWhenSlotFree(t *testing.T) {
	// With a free slot and aging on, a long-waiting LOW should outrank a
	// freshly-queued NORMAL for promotion.
	items := []Download{
		qd(1, StatusQueued, PriorityLow, 600, 0),   // 10h waiting
		qd(2, StatusQueued, PriorityNormal, 1, 0),  // just queued
	}
	st := SchedSettings{MaxActive: 1, AgingStepMin: 60, AgingCap: 150}
	want := schedulePlan(items, st, schedNow)
	// LOW base 1*1000 + aging min(150, 600/60=10) = 1010; NORMAL = 2000 → NORMAL still wins.
	if !want[2] {
		t.Fatalf("normal still outranks 10h-low under these knobs, got %v", want)
	}
	// But a HUGE wait + high cap crosses the tier:
	items[0] = qd(1, StatusQueued, PriorityLow, 60*1200, 0) // 1200h
	st.AgingCap = 1500
	want = schedulePlan(items, st, schedNow)
	if !want[1] {
		t.Fatalf("an extremely starved low should eventually outrank normal, got %v", want)
	}
}

func TestSchedulePlan_BootstrapTrimsExcessActives(t *testing.T) {
	// More downloading rows than MaxActive (legacy state after a restart):
	// only the strongest MaxActive stay; weakest are dropped (worker demotes).
	items := []Download{
		qd(1, StatusDownloading, PriorityLow, 0, 0),
		qd(2, StatusDownloading, PriorityHigh, 0, 0),
		qd(3, StatusDownloading, PriorityNormal, 0, 0),
	}
	want := schedulePlan(items, SchedSettings{MaxActive: 2}, schedNow)
	if len(want) != 2 || !want[2] || !want[3] || want[1] {
		t.Fatalf("expected HIGH #2 + NORMAL #3 kept, LOW #1 dropped, got %v", want)
	}
}

func TestSchedulePlan_FewerStallsWinTie(t *testing.T) {
	items := []Download{
		qd(1, StatusQueued, PriorityNormal, 10, 3), // more stalls
		qd(2, StatusQueued, PriorityNormal, 10, 0), // fewer stalls, same age
	}
	want := schedulePlan(items, SchedSettings{MaxActive: 1}, schedNow)
	if !want[2] || want[1] {
		t.Fatalf("fewer-stalls #2 should win the tie, got %v", want)
	}
}

func TestSchedulePlan_DefaultMaxActiveWhenUnset(t *testing.T) {
	items := []Download{
		qd(1, StatusQueued, PriorityNormal, 5, 0),
		qd(2, StatusQueued, PriorityNormal, 4, 0),
		qd(3, StatusQueued, PriorityNormal, 3, 0),
		qd(4, StatusQueued, PriorityNormal, 2, 0),
	}
	want := schedulePlan(items, SchedSettings{MaxActive: 0}, schedNow) // 0 → default 3
	if len(want) != 3 {
		t.Fatalf("expected default 3 active, got %d", len(want))
	}
}

func TestSchedulePlan_EmptyAndAllTerminalSafe(t *testing.T) {
	if got := schedulePlan(nil, SchedSettings{MaxActive: 3}, schedNow); len(got) != 0 {
		t.Fatalf("nil input should yield empty plan, got %v", got)
	}
}

func TestAssignQueuePositions(t *testing.T) {
	older := schedNow.Add(-10 * time.Minute)
	newer := schedNow.Add(-1 * time.Minute)
	list := []Download{
		{ID: 1, Status: StatusQueued, Priority: PriorityNormal, QueuedSince: &older},
		{ID: 2, Status: StatusDownloading, Priority: PriorityHigh}, // not queued → pos 0
		{ID: 3, Status: StatusQueued, Priority: PriorityHigh, QueuedSince: &newer},
		{ID: 4, Status: StatusQueued, Priority: PriorityNormal, QueuedSince: &newer},
	}
	AssignQueuePositions(list)
	pos := map[int]int{}
	for _, d := range list {
		pos[d.ID] = d.QueuePosition
	}
	// High (#3) first, then normals by age: #1 (older) before #4 (newer).
	if pos[3] != 1 || pos[1] != 2 || pos[4] != 3 {
		t.Fatalf("unexpected queue positions: %+v", pos)
	}
	if pos[2] != 0 {
		t.Errorf("downloading row should have position 0, got %d", pos[2])
	}
}

func TestAssignQueuePositions_NoQueued(t *testing.T) {
	list := []Download{{ID: 1, Status: StatusCompleted}, {ID: 2, Status: StatusDownloading}}
	AssignQueuePositions(list) // must not panic
	for _, d := range list {
		if d.QueuePosition != 0 {
			t.Errorf("#%d should keep position 0", d.ID)
		}
	}
}

func TestAgingBonus_EdgeCases(t *testing.T) {
	d := qd(1, StatusQueued, PriorityLow, 600, 0) // 10h ago
	// Disabled when step or cap is zero.
	if b := agingBonus(d, SchedSettings{AgingStepMin: 0, AgingCap: 100}, schedNow); b != 0 {
		t.Errorf("step=0 should disable aging, got %d", b)
	}
	if b := agingBonus(d, SchedSettings{AgingStepMin: 60, AgingCap: 0}, schedNow); b != 0 {
		t.Errorf("cap=0 should disable aging, got %d", b)
	}
	// Capped.
	if b := agingBonus(d, SchedSettings{AgingStepMin: 60, AgingCap: 3}, schedNow); b != 3 {
		t.Errorf("expected capped at 3, got %d", b)
	}
	// No queued_since → 0.
	if b := agingBonus(Download{Status: StatusQueued}, SchedSettings{AgingStepMin: 60, AgingCap: 100}, schedNow); b != 0 {
		t.Errorf("nil queued_since → 0, got %d", b)
	}
	// Future queued_since (negative age) → 0.
	future := schedNow.Add(time.Hour)
	if b := agingBonus(Download{QueuedSince: &future}, SchedSettings{AgingStepMin: 60, AgingCap: 100}, schedNow); b != 0 {
		t.Errorf("future queued_since → 0, got %d", b)
	}
}

func TestQueuedAt_FallsBackToCreatedAt(t *testing.T) {
	created := schedNow.Add(-time.Hour)
	d := Download{ID: 1, Status: StatusQueued, CreatedAt: created} // no QueuedSince
	if got := queuedAt(d); !got.Equal(created) {
		t.Errorf("expected fallback to created_at %v, got %v", created, got)
	}
	qs := schedNow.Add(-time.Minute)
	d.QueuedSince = &qs
	if got := queuedAt(d); !got.Equal(qs) {
		t.Errorf("expected queued_since %v, got %v", qs, got)
	}
}
