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
// The UNIT is the TORRENT, not the file: rows are folded into per-(user,
// info_hash) groups and ONE group occupies ONE slot, so a 389-file season pack
// counts the same as a single-file download (the OOM fix). A group's rank is its
// STRONGEST member's (representative). The returned set is EXPANDED back to every
// member ID of the chosen groups — so a single-file download (group of one) and
// every existing scheduler test (distinct/empty info_hash → group of one) behave
// byte-for-byte as before slot-per-line counting.
//
//   - Incumbent groups (any member already downloading) keep their slot unless a
//     waiting group of STRICTLY higher base priority preempts the weakest one.
//     Preemption ignores aging, so same-priority groups never thrash.
//   - Free slots go to the waiting group with the best promotion order (base
//     priority + aging; ties: fewer stalls, older queued_since, lower id).
//   - Bootstrap safety: if more than MaxActive groups are already downloading
//     (e.g. legacy rows after a restart), only the strongest MaxActive stay in
//     the returned set; the worker demotes the rest.
//
// Returns the set of download IDs that should be `downloading`.
func schedulePlan(items []Download, st SchedSettings, now time.Time) map[int]bool {
	if st.MaxActive <= 0 {
		st.MaxActive = 3
	}
	groups := GroupRows(items)
	var incumbents, waiting []scheduledGroup
	for _, g := range groups {
		rep, downloading := g.representative(st, now)
		sg := scheduledGroup{group: g, rep: rep}
		if downloading {
			incumbents = append(incumbents, sg)
		} else {
			waiting = append(waiting, sg)
		}
	}
	// Strongest incumbents first — if bootstrap left more than MaxActive running,
	// the weakest fall out of the want set (and the worker demotes them).
	sort.SliceStable(incumbents, func(i, j int) bool {
		return priorityBase(incumbents[i].rep.Priority) > priorityBase(incumbents[j].rep.Priority)
	})
	sort.SliceStable(waiting, func(i, j int) bool {
		return promotionLess(waiting[i].rep, waiting[j].rep, st, now)
	})
	f := newSlotFiller(st.MaxActive, st.PerUserMax)
	for _, sg := range incumbents {
		if f.hasGlobalRoom() && f.underPerUser(sg.rep.UserID) {
			f.take(sg)
		}
	}
	for _, sg := range waiting {
		if f.hasGlobalRoom() && f.underPerUser(sg.rep.UserID) {
			f.take(sg)
			continue
		}
		f.tryPreempt(sg)
	}
	return f.want
}

// scheduledGroup pairs a Group with its representative row (the strongest member
// that drives the group's scheduling rank). The slotFiller works on these so one
// group consumes one slot regardless of how many files it owns.
type scheduledGroup struct {
	group Group
	rep   Download
}

// slotFiller assembles the active set under two limits: a global ceiling and an
// optional per-user cap. The unit is the GROUP (one torrent), so each take/
// preempt consumes ONE slot but expands to every member ID in `want`. Methods are
// small so the scheduler stays simple.
type slotFiller struct {
	maxActive    int
	perUserMax   int // 0 = unlimited
	active       []scheduledGroup
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

// take admits a whole group into one slot, marking every member ID wanted.
func (f *slotFiller) take(sg scheduledGroup) {
	f.active = append(f.active, sg)
	for _, id := range sg.group.memberIDs() {
		f.want[id] = true
	}
	f.perUserCount[sg.rep.UserID]++
}

// tryPreempt swaps the weakest preemptable incumbent GROUP for candidate when the
// candidate has strictly higher base priority AND is below its per-user cap.
func (f *slotFiller) tryPreempt(candidate scheduledGroup) {
	if !f.underPerUser(candidate.rep.UserID) {
		return
	}
	i := f.weakestPreemptable(candidate)
	if i < 0 {
		return
	}
	victim := f.active[i]
	for _, id := range victim.group.memberIDs() {
		delete(f.want, id)
	}
	f.perUserCount[victim.rep.UserID]--
	f.active[i] = candidate
	for _, id := range candidate.group.memberIDs() {
		f.want[id] = true
	}
	f.perUserCount[candidate.rep.UserID]++
}

// weakestPreemptable returns the index of the lowest-priority *incumbent* group
// (one with a downloading member) that candidate may preempt (strictly higher
// base priority), or -1. Freshly-promoted waiters in active are never preempted.
func (f *slotFiller) weakestPreemptable(candidate scheduledGroup) int {
	weakest := -1
	for i, a := range f.active {
		if !groupHasDownloading(a.group) {
			continue
		}
		if weakest == -1 || priorityBase(a.rep.Priority) < priorityBase(f.active[weakest].rep.Priority) {
			weakest = i
		}
	}
	if weakest >= 0 && priorityBase(candidate.rep.Priority) > priorityBase(f.active[weakest].rep.Priority) {
		return weakest
	}
	return -1
}

// groupHasDownloading reports whether any member of the group is downloading —
// the group counts as a real incumbent (preemptable), versus a waiting group the
// slotFiller just promoted this pass.
func groupHasDownloading(g Group) bool {
	for _, m := range g.Members {
		if m.Status == StatusDownloading {
			return true
		}
	}
	return false
}
