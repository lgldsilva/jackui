package downloads

import (
	"sort"
	"time"
)

// SchedSettings are the live, user-tunable knobs the scheduler reads each tick.
type SchedSettings struct {
	MaxActive    int // GLOBAL ceiling: max concurrent downloads across all users (excludes streaming)
	PerUserMax   int // per-user concurrent cap; 0 = no per-user limit
	AgingStepMin int // minutes of waiting per +1 aging bonus (0 disables aging)
	AgingCap     int // ceiling on the aging bonus
}

// priorityBase maps a priority label to a rank. Higher = scheduled first.
func priorityBase(p string) int {
	switch p {
	case PriorityHigh:
		return 3
	case PriorityLow:
		return 1
	default:
		return 2 // normal / unknown
	}
}

// agingBonus grows with how long a queued row has waited, so a low-priority
// download isn't starved forever behind a stream of higher-priority ones. Only
// applies to waiting (queued) rows. Capped to keep it from dwarfing priority.
func agingBonus(d Download, st SchedSettings, now time.Time) int {
	if st.AgingStepMin <= 0 || st.AgingCap <= 0 || d.QueuedSince == nil {
		return 0
	}
	ageMin := int(now.Sub(*d.QueuedSince).Minutes())
	if ageMin <= 0 {
		return 0
	}
	b := ageMin / st.AgingStepMin
	if b > st.AgingCap {
		b = st.AgingCap
	}
	return b
}

// queuedAt is the fair-ordering timestamp for a row (queued_since, or created_at
// as a fallback for legacy rows that predate the column).
func queuedAt(d Download) time.Time {
	if d.QueuedSince != nil {
		return *d.QueuedSince
	}
	return d.CreatedAt
}

// promotionLess reports whether waiting row a should be promoted before b.
func promotionLess(a, b Download, st SchedSettings, now time.Time) bool {
	sa := priorityBase(a.Priority)*1000 + agingBonus(a, st, now)
	sb := priorityBase(b.Priority)*1000 + agingBonus(b, st, now)
	if sa != sb {
		return sa > sb
	}
	if a.Stalls != b.Stalls {
		return a.Stalls < b.Stalls // fewer past stalls first
	}
	if ta, tb := queuedAt(a), queuedAt(b); !ta.Equal(tb) {
		return ta.Before(tb) // older waiter first (FIFO within a tier)
	}
	return a.ID < b.ID
}

// AssignQueuePositions sets QueuePosition (1-based) on the queued rows in the
// list, in promotion order (priority, then oldest queued_since, then id).
// Non-queued rows keep position 0. Used by the list API to show "Nº in queue".
// It approximates the scheduler order (omits aging, which only matters for
// long-starved rows) so list handlers don't have to thread live settings.
func AssignQueuePositions(list []Download) {
	queued := make([]*Download, 0, len(list))
	for i := range list {
		if list[i].Status == StatusQueued {
			queued = append(queued, &list[i])
		}
	}
	sort.SliceStable(queued, func(i, j int) bool {
		a, b := queued[i], queued[j]
		if pa, pb := priorityBase(a.Priority), priorityBase(b.Priority); pa != pb {
			return pa > pb
		}
		if ta, tb := queuedAt(*a), queuedAt(*b); !ta.Equal(tb) {
			return ta.Before(tb)
		}
		return a.ID < b.ID
	})
	for i, d := range queued {
		d.QueuePosition = i + 1
	}
}

// schedulePlan decides which downloads should be in `downloading`, given the
// current schedulable set (downloading + queued) and the active limit. Pure and
// deterministic — `now` is passed in — so it's unit-testable without a clock.
//
//   - Incumbents (already downloading) keep their slot unless a waiting row of
//     STRICTLY higher base priority preempts the weakest incumbent. Preemption
//     ignores aging, so same-priority downloads never thrash.
//   - Free slots go to the waiting row with the best promotion order (base
//     priority + aging; ties: fewer stalls, older queued_since, lower id).
//   - Bootstrap safety: if more than MaxActive rows are already downloading
//     (e.g. legacy rows after a restart), only the strongest MaxActive stay in
//     the returned set; the worker demotes the rest.
//
// Returns the set of download IDs that should be `downloading`.
func schedulePlan(items []Download, st SchedSettings, now time.Time) map[int]bool {
	if st.MaxActive <= 0 {
		st.MaxActive = 3
	}
	var incumbents, waiting []Download
	for _, d := range items {
		if d.Status == StatusDownloading {
			incumbents = append(incumbents, d)
		} else {
			waiting = append(waiting, d)
		}
	}
	// Strongest incumbents first — if bootstrap left more than MaxActive running,
	// the weakest fall out of the want set (and the worker demotes them).
	sort.SliceStable(incumbents, func(i, j int) bool {
		return priorityBase(incumbents[i].Priority) > priorityBase(incumbents[j].Priority)
	})
	sort.SliceStable(waiting, func(i, j int) bool {
		return promotionLess(waiting[i], waiting[j], st, now)
	})
	f := newSlotFiller(st.MaxActive, st.PerUserMax)
	for _, d := range incumbents {
		if f.hasGlobalRoom() && f.underPerUser(d.UserID) {
			f.take(d)
		}
	}
	for _, w := range waiting {
		if f.hasGlobalRoom() && f.underPerUser(w.UserID) {
			f.take(w)
			continue
		}
		f.tryPreempt(w)
	}
	return f.want
}

// slotFiller assembles the active set under two limits: a global ceiling and an
// optional per-user cap. Methods are small so the scheduler stays simple.
type slotFiller struct {
	maxActive    int
	perUserMax   int // 0 = unlimited
	active       []Download
	want         map[int]bool
	perUserCount map[int]int
}

func newSlotFiller(maxActive, perUserMax int) *slotFiller {
	return &slotFiller{
		maxActive:    maxActive,
		perUserMax:   perUserMax,
		want:         make(map[int]bool, maxActive),
		perUserCount: make(map[int]int),
	}
}

func (f *slotFiller) hasGlobalRoom() bool { return len(f.active) < f.maxActive }

// underPerUser reports whether userID is below its per-user cap (or no cap set).
func (f *slotFiller) underPerUser(userID int) bool {
	return f.perUserMax <= 0 || f.perUserCount[userID] < f.perUserMax
}

func (f *slotFiller) take(d Download) {
	f.active = append(f.active, d)
	f.want[d.ID] = true
	f.perUserCount[d.UserID]++
}

// tryPreempt swaps the weakest preemptable incumbent for candidate when the
// candidate has strictly higher base priority AND is below its per-user cap.
func (f *slotFiller) tryPreempt(candidate Download) {
	if !f.underPerUser(candidate.UserID) {
		return
	}
	i := f.weakestPreemptable(candidate)
	if i < 0 {
		return
	}
	victim := f.active[i]
	delete(f.want, victim.ID)
	f.perUserCount[victim.UserID]--
	f.active[i] = candidate
	f.want[candidate.ID] = true
	f.perUserCount[candidate.UserID]++
}

// weakestPreemptable returns the index of the lowest-priority *incumbent* (real
// downloading row) that candidate may preempt (strictly higher base priority),
// or -1. Freshly-promoted waiters in active are never preempted.
func (f *slotFiller) weakestPreemptable(candidate Download) int {
	weakest := -1
	for i, a := range f.active {
		if a.Status != StatusDownloading {
			continue
		}
		if weakest == -1 || priorityBase(a.Priority) < priorityBase(f.active[weakest].Priority) {
			weakest = i
		}
	}
	if weakest >= 0 && priorityBase(candidate.Priority) > priorityBase(f.active[weakest].Priority) {
		return weakest
	}
	return -1
}
