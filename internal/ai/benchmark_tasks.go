package ai

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Multi-task benchmark. The benchmark used to measure a SINGLE task (the rename
// extraction) and rank the chain on it alone — so the chain was optimized for one
// job and could put at the top a model that's great at titles but terrible at, say,
// schedule parsing (exactly the bug that let "Toda segunda-feira às 07h00" fall to
// daily). The framework below makes the benchmark task-aware: each case carries a
// Task, every task has its own prompt+parse+score, and the per-slot score now has a
// per-task accuracy breakdown plus a composite that averages the task accuracies.
//
// Backwards-compat is deliberate: a case with an empty Task is the rename task (the
// historical default), so any custom/legacy set the user saved via PUT keeps scoring
// exactly as before, and the SlotScore JSON the UI reads is only EXTENDED (the new
// per-task field is optional).

// Task ids. TaskRename is the default (empty Task string normalizes to it) so the
// existing single-task dataset and any user-saved set keep working unchanged.
const (
	TaskRename   = "rename"   // full metadata extraction (title+year+kind+season+episode)
	TaskIdentify = "identify" // title-only extraction (categoryFromAI / art fail-safe)
	TaskSchedule = "schedule" // natural-language schedule → {kind,weekday,hour,minute}
)

// Schedule kinds, mirrored from watchlist.Schedule. Duplicated as plain strings
// here so the ai package stays decoupled from watchlist (same reason ScheduleResult
// lives in this package); the values MUST match watchlist.Sched* — guarded by a
// test that builds a watchlist.Schedule from a parsed result.
const (
	schedInterval = "interval"
	schedDaily    = "daily"
	schedWeekly   = "weekly"
)

// normalizeTask maps the empty/whitespace Task to the historical default so legacy
// cases (no Task column) and the UI's plain-title textarea both mean "rename".
func normalizeTask(task string) string {
	t := strings.TrimSpace(strings.ToLower(task))
	if t == "" {
		return TaskRename
	}
	return t
}

// taskRunner runs ONE case through ONE slot for a specific task: it sends the
// task's system prompt, times the call, and scores the parsed reply against the
// case's Expect label. accuracy is 0..1, tokens feeds the local-energy estimate,
// and err distinguishes transient/infra failures (skip) from bad output (score 0)
// exactly like the rename path — the shared scoreSingleCase logic relies on it.
type taskRunner func(ctx context.Context, c *Client, s Slot, expect, raw string) (accuracy float64, latency time.Duration, tokens int, err error)

// taskRunners is the registry the benchmark dispatches on. Adding a task here (a
// prompt + parse + score) is all it takes to fold it into the multi-task ranking —
// no change to the run loop, the store, or the API shape.
var taskRunners = map[string]taskRunner{
	TaskRename:   runRenameCase,
	TaskIdentify: runIdentifyCase,
	TaskSchedule: runScheduleCase,
}

// runnerFor returns the runner for a task, defaulting to rename for unknown/empty
// ids so a stray task value never silently drops a case.
func runnerFor(task string) taskRunner {
	if r, ok := taskRunners[normalizeTask(task)]; ok {
		return r
	}
	return runRenameCase
}

// runRenameCase: the original benchmark task — full rename metadata, scored by
// caseAccuracy (title 60% + season/episode 40%, year not penalized).
func runRenameCase(ctx context.Context, c *Client, s Slot, expect, raw string) (float64, time.Duration, int, error) {
	res, latency, tokens, err := c.metadataWithSlot(ctx, s, raw)
	if err != nil {
		return 0, latency, tokens, err
	}
	return caseAccuracy(res, expect), latency, tokens, nil
}

// runIdentifyCase: the title-only identify path (categoryFromAI + art fail-safe),
// scored on the title alone via titleAccuracy. Expect is the canonical title (the
// "- YYYY" tail, if any, is informational and ignored — identify returns no season).
func runIdentifyCase(ctx context.Context, c *Client, s Slot, expect, raw string) (float64, time.Duration, int, error) {
	res, latency, err := c.identifyWithSlot(ctx, s, raw)
	if err != nil {
		return 0, latency, 0, err
	}
	return titleAccuracy(res.Title, parseExpect(expect).Title), latency, 0, nil
}

// runScheduleCase: the watchlist schedule parse, scored by scheduleAccuracy. Expect
// is the canonical schedule label parsed by parseScheduleExpect (see below).
func runScheduleCase(ctx context.Context, c *Client, s Slot, expect, raw string) (float64, time.Duration, int, error) {
	content, latency, tokens, err := c.chat(ctx, s, scheduleSystem, raw, true)
	if err != nil {
		return 0, latency, tokens, err
	}
	res, perr := parseScheduleJSON(content)
	if perr != nil {
		return 0, latency, tokens, errBadOutput
	}
	// Apply the SAME safety net production uses, so the benchmark scores the real
	// end-to-end behavior (a model that drops the weekday but is corrected scores
	// like production does).
	fixDailyWithNamedWeekday(res, raw)
	return scheduleAccuracy(res, expect), latency, tokens, nil
}

// scheduleExpect is the structured form of a schedule Expect label.
type scheduleExpect struct {
	Kind    string
	Minutes int
	Weekday int
	Hour    int
	Minute  int
}

// parseScheduleExpect reads a canonical schedule label. The label format mirrors
// the strict JSON the parser produces, in a compact human-editable form:
//
//	interval:<minutes>            e.g. "interval:180"
//	daily:<HH>:<MM>               e.g. "daily:21:30"
//	weekly:<weekday>:<HH>:<MM>    e.g. "weekly:1:7:0"  (weekday 0=Sun … 6=Sat)
//
// An unrecognized label yields an empty Kind, which scheduleAccuracy treats as a
// case that can only fail — surfacing a malformed dataset entry instead of hiding it.
func parseScheduleExpect(expect string) scheduleExpect {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(expect)), ":")
	atoi := func(i int) int {
		if i < len(parts) {
			n, _ := strconv.Atoi(strings.TrimSpace(parts[i]))
			return n
		}
		return 0
	}
	switch parts[0] {
	case schedInterval:
		return scheduleExpect{Kind: schedInterval, Minutes: atoi(1)}
	case schedDaily:
		return scheduleExpect{Kind: schedDaily, Hour: atoi(1), Minute: atoi(2)}
	case schedWeekly:
		return scheduleExpect{Kind: schedWeekly, Weekday: atoi(1), Hour: atoi(2), Minute: atoi(3)}
	}
	return scheduleExpect{}
}

// scheduleAccuracy scores a parsed schedule against the expected label. The KIND
// must match for any credit (a daily that should be weekly is the bug we're guarding
// against and scores 0). Within the right kind, the defining fields are checked:
//   - interval: the period (minutes) — full credit if it matches, else 0.5 for
//     getting the kind right but the period wrong.
//   - daily:    hour (0.5) + minute (0.5) of the right kind's 1.0; kind alone = 0.5.
//   - weekly:   weekday is the dominant signal (it's what the bug got wrong), so
//     weekday 0.6 + hour 0.25 + minute 0.15, on top of the kind being right.
//
// Scoring weekday heavily makes the benchmark actually penalize a model that, on
// "toda segunda-feira", returns the wrong day (or daily) — the whole point of (B).
func scheduleAccuracy(res *ScheduleResult, expect string) float64 {
	if res == nil {
		return 0
	}
	want := parseScheduleExpect(expect)
	if want.Kind == "" || res.Kind != want.Kind {
		return 0
	}
	switch want.Kind {
	case schedInterval:
		if res.Minutes == want.Minutes {
			return 1
		}
		return 0.5
	case schedDaily:
		return 0.5*boolScore(res.Hour == want.Hour) + 0.5*boolScore(res.Minute == want.Minute)
	case schedWeekly:
		return 0.6*boolScore(res.Weekday == want.Weekday) +
			0.25*boolScore(res.Hour == want.Hour) +
			0.15*boolScore(res.Minute == want.Minute)
	}
	return 0
}
