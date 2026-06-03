package downloads

import (
	"sort"
	"time"
)

// SchedSettings are the live, user-tunable knobs the scheduler reads each tick.
type SchedSettings struct {
	MaxActive    int // max concurrent downloads (excludes streaming)
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
	return fillActiveSlots(incumbents, waiting, st.MaxActive)
}

// fillActiveSlots builds the final active set: incumbents first (capped at
// maxActive), then waiting rows into free slots, then strict-priority preemption.
// Inputs must already be sorted by desirability.
func fillActiveSlots(incumbents, waiting []Download, maxActive int) map[int]bool {
	want := make(map[int]bool, maxActive)
	active := make([]Download, 0, maxActive)
	for _, d := range incumbents {
		if len(active) >= maxActive {
			break
		}
		active = append(active, d)
		want[d.ID] = true
	}
	for _, w := range waiting {
		if len(active) < maxActive {
			active = append(active, w)
			want[w.ID] = true
			continue
		}
		if i := preemptableIndex(active, w); i >= 0 {
			delete(want, active[i].ID)
			active[i] = w
			want[w.ID] = true
		}
	}
	return want
}

// preemptableIndex returns the index in active of the weakest *incumbent* (real
// downloading row) that `candidate` may preempt — only when candidate has
// strictly higher base priority. Returns -1 when no preemption is warranted.
// Freshly-promoted waiters in `active` are never preempted.
func preemptableIndex(active []Download, candidate Download) int {
	weakest := -1
	for i, a := range active {
		if a.Status != StatusDownloading {
			continue
		}
		if weakest == -1 || priorityBase(a.Priority) < priorityBase(active[weakest].Priority) {
			weakest = i
		}
	}
	if weakest >= 0 && priorityBase(candidate.Priority) > priorityBase(active[weakest].Priority) {
		return weakest
	}
	return -1
}
